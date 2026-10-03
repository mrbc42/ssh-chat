package sshserver

import (
	"context"
	"log"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
)

// teaMiddleware is wish's bubbletea middleware with one fix.
//
// wish forwards window-resize events to the program in a loop that does
// `case w := <-windowChanges:`. When a client closes its session channel but
// keeps the SSH connection open, that channel is CLOSED, a receive on a closed
// channel returns instantly, and the loop spins forever at full CPU sending
// 0x0 resize messages — each one making the program re-lay-out its whole
// screen. A client (or a load-test tool that leaks connections) can pin a core
// per connection that way. Here a closed channel ends the program instead.
func teaMiddleware(handler bm.Handler) wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			m, opts := handler(sess)
			if m == nil {
				next(sess)
				return
			}
			program := tea.NewProgram(m, append(opts, bm.MakeOptions(sess)...)...)
			_, windowChanges, ok := sess.Pty()
			if !ok {
				wish.Fatalln(sess, "no active terminal, skipping")
				return
			}
			ctx, cancel := context.WithCancel(sess.Context())
			go forwardWindowChanges(ctx, windowChanges, program.Send, program.Quit)
			if _, err := program.Run(); err != nil {
				log.Printf("tea program exited with error: %v", err)
			}
			// Kill forces the program down if it is still running.
			program.Kill()
			cancel()
			next(sess)
		}
	}
}

// forwardWindowChanges relays resize events to send until ctx ends or the
// client's window-change channel is closed, then calls quit.
func forwardWindowChanges(ctx context.Context, changes <-chan ssh.Window, send func(tea.Msg), quit func()) {
	for {
		select {
		case <-ctx.Done():
			quit()
			return
		case w, open := <-changes:
			if !open {
				quit()
				return
			}
			send(tea.WindowSizeMsg{Width: w.Width, Height: w.Height})
		}
	}
}
