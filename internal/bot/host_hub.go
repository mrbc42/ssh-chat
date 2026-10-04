package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mrbc42/ssh-chat/internal/hub"
	"github.com/mrbc42/ssh-chat/internal/store"
)

// botFP marks the bot's own identity. It looks like a keyed fingerprint so
// the chat server persists and reserves the bot's nickname.
const botFP = "SHA256:bot-sysop"

// hubHost connects a Bot to the chat hub: it is both the Host the bot speaks
// through and the hub.Observer that feeds it events.
type hubHost struct {
	nick   string
	ctx    context.Context
	h      *hub.Hub
	sess   *hub.Session
	events chan Event
}

// Start creates the bot, seats it in #main as a normal participant and runs
// it until ctx is cancelled. Returns the bot's nickname.
func Start(ctx context.Context, h *hub.Hub, st *store.Store, o Options) (string, error) {
	hh := &hubHost{ctx: ctx, h: h, events: make(chan Event, 256)}
	b, err := New(hh, o)
	if err != nil {
		return "", err
	}
	hh.nick = b.Nick()
	hh.sess = hub.NewSession(botFP, "", b.Nick())
	hh.sess.SetTrusted(true) // the bot enforces its own output limits
	// Reserve the nickname so no user can take it, even while the bot is down.
	if err := st.SetNickname(ctx, botFP, b.Nick()); err != nil {
		return "", fmt.Errorf("reserving bot nick: %w", err)
	}
	go func() { // nothing reads the bot's own screen
		for {
			select {
			case <-hh.sess.Outbox:
			case <-ctx.Done():
				return
			}
		}
	}()
	h.SetBotHelp(HelpLines(b.Nick()))
	h.SetObserver(hh)
	h.Main().Join(hh.sess)
	go b.Run(ctx, hh.events)
	return b.Nick(), nil
}

func (hh *hubHost) Say(text string)         { hub.HandleInput(hh.ctx, hh.h, hh.sess, text) }
func (hh *hubHost) SayIn(room, text string) { hh.h.SayIn(hh.sess, room, text) }
func (hh *hubHost) Action(text string)      { hub.HandleInput(hh.ctx, hh.h, hh.sess, "/me "+text) }
func (hh *hubHost) PM(nick, text string) {
	hub.HandleInput(hh.ctx, hh.h, hh.sess, "/msg "+nick+" "+text)
}

func (hh *hubHost) Online() []Person {
	var out []Person
	for _, s := range hh.h.Sessions() {
		if s != hh.sess {
			out = append(out, person(s))
		}
	}
	return out
}

// OnlineCount uses the hub's shared once-a-second count instead of walking
// every session, which the bot used to do on every single chat message.
func (hh *hubHost) ShowLines(nick string, lines []string) bool { return hh.h.ShowLines(nick, lines) }

func (hh *hubHost) ShowMotd(nick, text string) bool { return hh.h.ShowMotd(nick, text) }

func (hh *hubHost) TryPM(nick, text string) bool { return hh.h.TryPM(hh.sess, nick, text) }

func (hh *hubHost) IsAdmin(fp string) bool { return hh.h.IsAdmin(fp) }

func (hh *hubHost) OnlineCount() int { return max(hh.h.TotalUsersOnline()-1, 0) }

func person(s *hub.Session) Person {
	return Person{ID: strconv.FormatUint(s.ID(), 10), Nick: s.Nick(), FP: s.FP, Anon: strings.HasPrefix(s.FP, "anon-")}
}

// push never blocks a room goroutine; a full queue drops the event.
func (hh *hubHost) push(ev Event) {
	select {
	case hh.events <- ev:
	default:
	}
}

func (hh *hubHost) OnJoin(room string, s *hub.Session, login bool) {
	if s != hh.sess && login {
		hh.push(Event{Kind: EvLogin, P: person(s)})
	}
}

func (hh *hubHost) OnPart(room string, s *hub.Session, disconnect bool) {
	if s != hh.sess && disconnect {
		hh.push(Event{Kind: EvLogoff, P: person(s)})
	}
}

func (hh *hubHost) OnChat(room string, s *hub.Session, body string, action bool) {
	if s == hh.sess || action {
		return
	}
	// #main: the bot sees everything (greetings, activity). Other channels:
	// only what is meant for it, so a busy channel can't flood the bot's queue.
	if strings.EqualFold(room, store.MainChannelName) || addressesBot(body, hh.nick) {
		hh.push(Event{Kind: EvChat, P: person(s), Body: body, Room: room})
	}
}

// addressesBot is a cheap pre-filter for chat outside #main: a !command, or a
// message that names the bot ("@Nick", "Nick:", "Nick,").
func addressesBot(body, nick string) bool {
	t := strings.ToLower(strings.TrimSpace(body))
	n := strings.ToLower(nick)
	return strings.HasPrefix(t, "!") || strings.Contains(t, "@"+n) ||
		strings.HasPrefix(t, n+":") || strings.HasPrefix(t, n+",")
}
