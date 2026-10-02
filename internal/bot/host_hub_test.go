package bot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrbc42/ssh-chat/internal/hub"
	"github.com/mrbc42/ssh-chat/internal/store"
)

// lines collects everything a session's screen receives.
func collect(s *hub.Session, d time.Duration) []hub.Line {
	var out []hub.Line
	deadline := time.After(d)
	for {
		select {
		case o := <-s.Outbox:
			if o.Line != nil {
				out = append(out, *o.Line)
			}
		case <-deadline:
			return out
		}
	}
}

func fromBot(lines []hub.Line) (chat, pm []string) {
	for _, l := range lines {
		if l.Kind == hub.KindChat && l.Sender == "SysOp-Gus" {
			chat = append(chat, l.Body)
		}
		if l.Kind == hub.KindPM && l.Sender == "SysOp-Gus" {
			pm = append(pm, l.Body)
		}
	}
	return
}

func TestBotIsAParticipantInTheRealHub(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "chat.db"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := hub.NewHub(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dir, "data")
	_ = os.MkdirAll(data, 0o755)
	_ = os.WriteFile(filepath.Join(data, "config.json"), []byte(`{"typing_delay_ms":[0,0],"wrap":1000}`), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	nick, err := Start(ctx, h, st, Options{DBPath: filepath.Join(dir, "bot.db"), UnmatchedPath: filepath.Join(dir, "unmatched.log"), DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	if nick != "SysOp-Gus" {
		t.Fatalf("nick = %q", nick)
	}
	time.Sleep(200 * time.Millisecond)

	// 1. The bot is a participant: it is in the room's member list.
	if !contains2(h.Main().Who(), "SysOp-Gus") {
		t.Fatalf("bot not in #main: %v", h.Main().Who())
	}
	// ...and its nickname is reserved against everyone else.
	if h.NickAvailable(ctx, "sysop-gus", hub.NewSession("SHA256:x", "", "x")) {
		t.Fatal("bot nickname is not reserved")
	}

	// 2. Joining: a keyed user logs in and is greeted by name.
	dave := hub.NewSession("SHA256:dave", "1.1.1.1", "Dave")
	h.Main().Join(dave)
	chat, _ := fromBot(collect(dave, 1500*time.Millisecond))
	if !strings.Contains(strings.Join(chat, " "), "Dave") {
		t.Fatalf("no greeting for Dave: %v", chat)
	}

	// 3. Silence during normal user-to-user chat.
	erin := hub.NewSession("SHA256:erin", "2.2.2.2", "Erin")
	h.Main().Join(erin)
	collect(erin, 1200*time.Millisecond)
	collect(dave, 100*time.Millisecond)
	hub.HandleInput(ctx, h, dave, "hello Erin")
	hub.HandleInput(ctx, h, erin, "hi Dave, nice weather")
	hub.HandleInput(ctx, h, dave, "yeah, cooked a roast")
	time.Sleep(300 * time.Millisecond)
	// (each user's very first message gets a one-off reaction; after that: silence)
	collect(dave, 1200*time.Millisecond)
	hub.HandleInput(ctx, h, dave, "and now more chatter")
	hub.HandleInput(ctx, h, erin, "indeed, more chatter")
	chat, pm := fromBot(collect(dave, 1000*time.Millisecond))
	if len(chat)+len(pm) != 0 {
		t.Fatalf("bot spoke during normal chat: %v %v", chat, pm)
	}

	// 4. Addressed and commands get answered.
	hub.HandleInput(ctx, h, dave, "@SysOp-Gus !time")
	chat, _ = fromBot(collect(dave, 1000*time.Millisecond))
	if len(chat) != 1 || !strings.Contains(chat[0], ":") {
		t.Fatalf("no !time reply: %v", chat)
	}

	// 5. Leaving: a disconnect is announced.
	h.Main().PartDisconnect(erin)
	chat, _ = fromBot(collect(dave, 1500*time.Millisecond))
	if !strings.Contains(strings.Join(chat, " "), "Erin") {
		t.Fatalf("no logoff announcement for Erin: %v", chat)
	}

	// 6. !tell: stored for the offline user, delivered as a PM at next login.
	hub.HandleInput(ctx, h, dave, "!tell Erin the roast is ready")
	chat, _ = fromBot(collect(dave, 1200*time.Millisecond))
	if len(chat) != 1 || !strings.Contains(chat[0], "Erin") {
		t.Fatalf("tell not acknowledged: %v", chat)
	}
	erin2 := hub.NewSession("SHA256:erin", "2.2.2.2", "Erin")
	h.Main().Join(erin2)
	_, pm = fromBot(collect(erin2, 2*time.Second))
	if len(pm) != 1 || !strings.Contains(pm[0], "the roast is ready") {
		t.Fatalf("tell not delivered at login: %v", pm)
	}

	// Chat history keeps the bot's lines out of /ignore's way: users can ignore it.
	hub.HandleInput(ctx, h, dave, "/ignore SysOp-Gus")
	collect(dave, 200*time.Millisecond)
	hub.HandleInput(ctx, h, dave, "@SysOp-Gus !time")
	chat, _ = fromBot(collect(dave, 800*time.Millisecond))
	if len(chat) != 0 {
		t.Fatalf("ignored bot still reached Dave: %v", chat)
	}
}

func contains2(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
