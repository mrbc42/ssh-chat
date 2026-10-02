package hub

import (
	"context"
	"strings"
	"testing"
	"time"
)

// errors drains s and returns every error line.
func errorsFor(s *Session) []string {
	var out []string
	time.Sleep(200 * time.Millisecond)
	for {
		select {
		case o := <-s.Outbox:
			if o.Line != nil && o.Line.Kind == KindError {
				out = append(out, o.Line.Body)
			}
		default:
			return out
		}
	}
}

func disconnected(s *Session) bool {
	time.Sleep(200 * time.Millisecond)
	for {
		select {
		case o := <-s.Outbox:
			if o.Disconnect {
				return true
			}
		default:
			return false
		}
	}
}

func TestChatFloodIsThrottledAndEventuallyCutOff(t *testing.T) {
	h, _ := adminHub(t)
	ctx := context.Background()
	flooder := NewSession("SHA256:flood", "6.6.6.6", "flooder")
	other := NewSession("SHA256:other", "7.7.7.7", "other")
	h.Main().Join(flooder)
	h.Main().Join(other)
	drain(flooder)

	for i := 0; i < 40; i++ { // a burst: only ~10 get through
		HandleInput(ctx, h, flooder, "spam "+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	errs := errorsFor(flooder)
	if len(errs) < 20 || !strings.Contains(errs[0], "too fast") {
		t.Fatalf("flood not throttled: %d errors %v", len(errs), errs)
	}
	got := 0
	time.Sleep(100 * time.Millisecond)
	for {
		select {
		case o := <-other.Outbox:
			if o.Line != nil && o.Line.Kind == KindChat {
				got++
			}
			continue
		default:
		}
		break
	}
	if got > 14 {
		t.Fatalf("%d of 40 flooded messages reached the room, burst limit is 10", got)
	}

	// Keep hammering: the session is cut off.
	for i := 0; i < maxStrikes+20; i++ {
		HandleInput(ctx, h, flooder, "more "+string(rune('a'+i%26))+string(rune('a'+i/26%26)))
	}
	if !disconnected(flooder) {
		t.Fatal("persistent flooder was not disconnected")
	}
}

func TestPastingABlockOfTextDoesNotDisconnect(t *testing.T) {
	h, _ := adminHub(t)
	ctx := context.Background()
	s := NewSession("SHA256:paste", "6.6.6.6", "paster")
	h.Main().Join(s)
	drain(s)
	for i := 0; i < 40; i++ { // a plausible big paste
		HandleInput(ctx, h, s, "line of pasted text number "+strings.Repeat("x", i))
	}
	if disconnected(s) {
		t.Fatal("a 40-line paste must not get the user thrown off")
	}
}

func TestRepeatedMessagesAreSuppressed(t *testing.T) {
	h, _ := adminHub(t)
	ctx := context.Background()
	s := NewSession("SHA256:rep", "6.6.6.6", "repeater")
	h.Main().Join(s)
	drain(s)
	for i := 0; i < 2; i++ { // two identical in a row is fine
		HandleInput(ctx, h, s, "lol")
	}
	if e := errorsFor(s); len(e) != 0 {
		t.Fatalf("two repeats refused: %v", e)
	}
	HandleInput(ctx, h, s, "LOL ")
	if e := errorsFor(s); len(e) != 1 || !strings.Contains(e[0], "repeat") {
		t.Fatalf("third identical message should be refused: %v", e)
	}
	HandleInput(ctx, h, s, "something else")
	if e := errorsFor(s); len(e) != 0 {
		t.Fatalf("different message refused: %v", e)
	}
}

func TestCommandsAreRateLimitedAndCooldowns(t *testing.T) {
	h, _ := adminHub(t)
	ctx := context.Background()
	s := NewSession("SHA256:cmd", "6.6.6.6", "cmdspam")
	h.Main().Join(s)
	drain(s)
	for i := 0; i < 30; i++ {
		HandleInput(ctx, h, s, "/flip")
	}
	errs := errorsFor(s)
	if len(errs) < 15 || !strings.Contains(errs[0], "commands too fast") {
		t.Fatalf("command flood not limited: %d %v", len(errs), errs)
	}

	// /nick has a cooldown after a successful change.
	s2 := NewSession("SHA256:nick", "6.6.6.7", "nicker")
	h.Main().Join(s2)
	drain(s2)
	if got := info(t, h, s2, "/nick first-name"); got != "" {
		t.Fatalf("first nick change refused: %s", got)
	}
	if got := info(t, h, s2, "/nick second-name"); !strings.Contains(got, "Please wait") {
		t.Fatalf("nick cooldown missing: %q", got)
	}
	if s2.Nick() != "first-name" {
		t.Fatalf("nick changed during cooldown: %s", s2.Nick())
	}

	// /admin has a cooldown too.
	if got := info(t, h, s2, "/admin help me"); !strings.Contains(got, "logged") && !strings.Contains(got, "sent") {
		t.Fatalf("first /admin report: %q", got)
	}
	if got := info(t, h, s2, "/admin again"); !strings.Contains(got, "Please wait") {
		t.Fatalf("admin cooldown missing: %q", got)
	}
}

func TestChannelCreationCaps(t *testing.T) {
	h, _ := adminHub(t)
	ctx := context.Background()
	s := NewSession("SHA256:maker", "6.6.6.6", "maker")
	h.Main().Join(s)
	drain(s)

	_ = info(t, h, s, "/create room-one")
	if got := info(t, h, s, "/create room-two"); !strings.Contains(got, "Please wait") {
		t.Fatalf("creation cooldown missing: %q", got)
	}
	// Caps: lift the cooldown and fill the allowance.
	for i := 1; i < maxOwnedChannels; i++ {
		s.StartCooldown("create", 0)
		if _, err := h.store.CreateChannel(ctx, "extra-"+string(rune('a'+i)), s.FP); err != nil {
			t.Fatal(err)
		}
	}
	s.StartCooldown("create", 0)
	if got := info(t, h, s, "/create one-too-many"); !strings.Contains(got, "limit") {
		t.Fatalf("per-user channel cap missing: %q", got)
	}
	s.StartCooldown("create", 0)
	if got := info(t, h, s, "/join also-too-many"); !strings.Contains(got, "limit") {
		t.Fatalf("/join must not bypass the channel cap: %q", got)
	}
}

func TestWholeChannelCap(t *testing.T) {
	h, _ := adminHub(t)
	ctx := context.Background()
	var users []*Session
	for i := 0; i < 12; i++ { // many connections, each within its own limit
		u := NewSession("SHA256:u"+string(rune('a'+i)), "1.1.1."+string(rune('a'+i)), "u"+string(rune('a'+i)))
		h.Main().Join(u)
		users = append(users, u)
	}
	for _, u := range users {
		drain(u)
	}
	for round := 0; round < 8; round++ {
		for i, u := range users {
			HandleInput(ctx, h, u, "msg "+string(rune('a'+round))+string(rune('a'+i)))
		}
	}
	// 96 messages from 12 users at once exceeds the 40-message channel burst.
	time.Sleep(300 * time.Millisecond)
	delivered := 0
	for {
		select {
		case o := <-users[0].Outbox:
			if o.Line != nil && o.Line.Kind == KindChat {
				delivered++
			}
			continue
		default:
		}
		break
	}
	if delivered > roomBurst+10 {
		t.Fatalf("channel cap not applied: %d delivered, burst is %d", delivered, roomBurst)
	}
}

func TestTrustedSessionIsExempt(t *testing.T) {
	h, _ := adminHub(t)
	ctx := context.Background()
	bot := NewSession("SHA256:bot", "", "robot")
	bot.SetTrusted(true)
	h.Main().Join(bot)
	drain(bot)
	for i := 0; i < 50; i++ {
		HandleInput(ctx, h, bot, "/flip")
	}
	if e := errorsFor(bot); len(e) != 0 {
		t.Fatalf("trusted session throttled: %v", e)
	}
}
