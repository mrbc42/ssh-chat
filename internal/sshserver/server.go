// Package sshserver wires wish's SSH middleware into the chat hub and
// bubbletea UI, with zero-credential auth (any key or no key is accepted).
package sshserver

import (
	"context"
	"net"

	tea "github.com/charmbracelet/bubbletea"
	bm "github.com/charmbracelet/wish/bubbletea"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	"github.com/charmbracelet/wish/logging"
	gossh "golang.org/x/crypto/ssh"

	"github.com/mrbc42/ssh-chat/internal/hub"
	"github.com/mrbc42/ssh-chat/internal/ratelimit"
	"github.com/mrbc42/ssh-chat/internal/store"
	"github.com/mrbc42/ssh-chat/internal/ui"
)

const maxConnsPerIP = 3

func New(addr, hostKeyPath string, st *store.Store, h *hub.Hub) (*ssh.Server, error) {
	connLimiter := ratelimit.NewConnLimiter(maxConnsPerIP)

	s, err := wish.NewServer(
		wish.WithAddress(addr),
		wish.WithHostKeyPath(hostKeyPath),
		// Zero-credential access: accept any offered public key...
		wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
			return true
		}),
		// ...and also accept connections that offer no auth method at all.
		wish.WithKeyboardInteractiveAuth(func(ctx ssh.Context, challenger gossh.KeyboardInteractiveChallenge) bool {
			return true
		}),
		wish.WithMiddleware(
			// Innermost: runs after the bubbletea Program.Run() returns (on
			// EOF/disconnect/quit), so the session is parted from whatever
			// room it was last in instead of lingering as a ghost member.
			disconnectMiddleware(),
			bm.Middleware(teaHandler(st, h)),
			activeterm.Middleware(),
			logging.Middleware(),
			ratelimit.Middleware(connLimiter),
		),
	)
	if err != nil {
		return nil, err
	}
	return s, nil
}

type sessionCtxKey struct{}

// disconnectMiddleware parts the chat session (stashed in the ssh.Context by
// teaHandler) from its current room once the bubbletea program for this
// connection has finished, whatever the reason (EOF, /quit, kick).
func disconnectMiddleware() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			next(sess)
			if chatSess, ok := sess.Context().Value(sessionCtxKey{}).(*hub.Session); ok {
				if room := chatSess.CurrentRoom(); room != nil {
					room.Part(chatSess)
				}
			}
		}
	}
}

func teaHandler(st *store.Store, h *hub.Hub) bm.Handler {
	return func(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
		ctx := context.Background()

		var fp string
		if pk := sess.PublicKey(); pk != nil {
			fp = gossh.FingerprintSHA256(pk)
		} else {
			fp = hub.RandomAnonFP()
		}
		ip := remoteIP(sess)

		nick, known, err := st.ResolveNickname(ctx, fp)
		if err != nil || !known {
			nick = hub.DefaultNickFor(fp)
			_ = st.SetNickname(ctx, fp, nick)
		}

		chatSess := hub.NewSession(fp, ip, nick)
		sess.Context().SetValue(sessionCtxKey{}, chatSess)

		pty, _, _ := sess.Pty()
		width, height := pty.Window.Width, pty.Window.Height
		if width <= 0 {
			width = 80
		}
		if height <= 0 {
			height = 24
		}

		renderer := bm.MakeRenderer(sess)
		model := ui.NewModel(ctx, h, chatSess, renderer, width, height)

		// Join #main before the bubbletea program starts, so the session's
		// Outbox already has the SwitchRoom+scrollback queued when the UI's
		// first Init() listener reads it.
		h.Main().Join(chatSess)

		return model, bm.MakeOptions(sess)
	}
}

func remoteIP(sess ssh.Session) string {
	addr := sess.RemoteAddr()
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}
