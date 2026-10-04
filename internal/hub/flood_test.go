package hub

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrbc42/ssh-chat/internal/store"
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

// The ghost-member race: a user's join to a room is still queued in that
// room's worker when their connection ends. Disconnect parts them from the
// room they were in at that moment, then the queued join is processed and
// (before the fix) added a dead session that nobody would ever remove.
func TestQueuedJoinAfterDisconnectDoesNotCreateGhost(t *testing.T) {
	h, st := adminHub(t)
	ctx := context.Background()
	ch, err := st.CreateChannel(ctx, "ghosttown", "SHA256:owner")
	if err != nil {
		t.Fatal(err)
	}
	other := h.GetOrLoadRoom(ctx, ch)

	s := NewSession("SHA256:leaver", "1.1.1.1", "leaver")
	h.Main().Join(s)
	time.Sleep(100 * time.Millisecond)

	other.Send(evJoin{sess: s}) // /join ghosttown: queued, not yet processed...
	s.MarkClosed()              // ...and the connection ends first
	h.Main().PartDisconnect(s)  // the disconnect parts the OLD room
	time.Sleep(200 * time.Millisecond)

	if got := other.Who(); len(got) != 0 {
		t.Fatalf("dead session became a member of #ghosttown: %v", got)
	}
	if got := h.Main().Who(); len(got) != 0 {
		t.Fatalf("dead session still in #main: %v", got)
	}
}

// Belt and braces: anything that slips through is reaped when its connection
// is found to be gone.
func TestJanitorReapsSessionsWhoseConnectionEnded(t *testing.T) {
	h, _ := adminHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	dead := NewSession("SHA256:dead", "1.1.1.1", "dead")
	dead.SetDone(done)
	live := NewSession("SHA256:live", "2.2.2.2", "live")
	live.SetDone(make(chan struct{}))
	h.Main().Join(dead)
	h.Main().Join(live)
	time.Sleep(100 * time.Millisecond)
	if n := len(h.Main().Who()); n != 2 {
		t.Fatalf("setup: %d members", n)
	}

	close(done) // the connection ended but nothing parted the session
	h.StartJanitor(ctx, 50*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for len(h.Main().Who()) != 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	who := h.Main().Who()
	if len(who) != 1 || who[0] != "live" {
		t.Fatalf("janitor should remove only the dead session, members now: %v", who)
	}
}

func TestHistoryLimitsAndChannelScope(t *testing.T) {
	h, st := adminHub(t)
	ctx := context.Background()
	main, _ := st.GetOrCreateMainChannel(ctx)
	side, err := st.CreateChannel(ctx, "side", "SHA256:owner")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 700; i++ {
		st.AppendMessage(ctx, store.Message{ChannelID: main.ID, SenderFP: "f", SenderName: "bob", Body: "main " + strconv.Itoa(i), Kind: "msg"})
	}
	for i := 0; i < 5; i++ {
		st.AppendMessage(ctx, store.Message{ChannelID: side.ID, SenderFP: "f", SenderName: "bob", Body: "side " + strconv.Itoa(i), Kind: "msg"})
	}
	s := NewSession("SHA256:reader", "", "reader")
	h.Main().Join(s)
	time.Sleep(100 * time.Millisecond)

	lines, exhausted, err := h.History(ctx, s, 0, 9999) // absurd n is clamped to the maximum
	if err != nil || len(lines) != maxHistory || exhausted {
		t.Fatalf("n should clamp to %d: got %d exhausted=%v err=%v", maxHistory, len(lines), exhausted, err)
	}
	newest := lines[len(lines)-1] // the newest stored line may be the join notice itself
	if newest.ID == 0 || (newest.Body != "main 699" && !strings.Contains(newest.Body, "has joined")) {
		t.Fatalf("newest history line = %+v", newest)
	}
	for i := 1; i < len(lines); i++ {
		if lines[i].ID <= lines[i-1].ID {
			t.Fatal("history must be oldest first with ascending ids")
		}
	}
	if lines, _, _ := h.History(ctx, s, 0, 0); len(lines) != defaultHistory {
		t.Fatalf("n=0 should mean the default %d, got %d", defaultHistory, len(lines))
	}

	other := NewSession("SHA256:other", "", "other") // in #side: sees only #side
	ch2, _, _ := st.GetChannelByName(ctx, "side")
	h.GetOrLoadRoom(ctx, ch2).Join(other)
	time.Sleep(100 * time.Millisecond)
	lines, exhausted, _ = h.History(ctx, other, 0, 100)
	// 5 messages, plus possibly the join notice for "other" itself.
	if (len(lines) != 5 && len(lines) != 6) || !exhausted || !strings.HasPrefix(lines[0].Body, "side") {
		t.Fatalf("#side history = %d lines exhausted=%v", len(lines), exhausted)
	}
	for _, l := range lines {
		if strings.HasPrefix(l.Body, "main") {
			t.Fatal("#side history leaked #main messages")
		}
	}
}

func TestTryPMReportsRealDelivery(t *testing.T) {
	h, _ := adminHub(t)
	bot := NewSession("SHA256:bot", "", "robot")
	bot.SetTrusted(true)
	reader := NewSession("SHA256:reader", "1.1.1.1", "reader")
	h.Main().Join(bot)
	h.Main().Join(reader)
	time.Sleep(100 * time.Millisecond)
	drain(reader)

	if h.TryPM(bot, "nobody", "hi") {
		t.Fatal("a PM to an offline user must report not delivered")
	}
	if !h.TryPM(bot, "reader", "mail for you") {
		t.Fatal("a PM to an online user should be delivered")
	}
	got := false
	for _, l := range collectLines(reader) {
		got = got || (l.Kind == KindPM && l.Body == "mail for you" && l.Sender == "robot")
	}
	if !got {
		t.Fatal("the reader never received the PM")
	}

	reader.Ignore("robot") // the reader ignores the sender: report false, deliver nothing
	drain(reader)
	if h.TryPM(bot, "reader", "ignored mail") {
		t.Fatal("a PM to a user ignoring the sender must report not delivered")
	}
	for _, l := range collectLines(reader) {
		if l.Kind == KindPM {
			t.Fatalf("an ignored sender's PM was delivered: %q", l.Body)
		}
	}
}

func collectLines(s *Session) []Line {
	time.Sleep(150 * time.Millisecond)
	var out []Line
	for {
		select {
		case o := <-s.Outbox:
			if o.Line != nil {
				out = append(out, *o.Line)
			}
		default:
			return out
		}
	}
}

func TestShowMotdGoesToOneUserEvenIfTheyIgnoreTheBot(t *testing.T) {
	h, _ := adminHub(t)
	a := NewSession("SHA256:a", "1.1.1.1", "alice")
	b := NewSession("SHA256:b", "2.2.2.2", "bob")
	h.Main().Join(a)
	h.Main().Join(b)
	time.Sleep(100 * time.Millisecond)
	a.Ignore("SysOp-Gus") // the user has ignored the bot: the MOTD is still server information
	drain(a)
	drain(b)

	if !h.ShowMotd("alice", "Be excellent to each other") {
		t.Fatal("ShowMotd to an online user should report delivered")
	}
	if h.ShowMotd("nobody", "x") {
		t.Fatal("ShowMotd to an offline user must report not delivered")
	}
	got := 0
	for _, l := range collectLines(a) {
		if l.Kind == KindMotd && l.Body == "Be excellent to each other" {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("alice should receive exactly one MOTD block, got %d", got)
	}
	for _, l := range collectLines(b) {
		if l.Kind == KindMotd {
			t.Fatal("the MOTD must go to the one user only")
		}
	}
}
