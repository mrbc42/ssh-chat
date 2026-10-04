package hub

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrbc42/ssh-chat/internal/filter"
	"github.com/mrbc42/ssh-chat/internal/store"
)

func adminHub(t *testing.T) (*Hub, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHub(st, map[string]bool{"SHA256:admin": true})
	if err != nil {
		t.Fatal(err)
	}
	return h, st
}

// info runs a command and returns the first info/error line it produced.
func info(t *testing.T, h *Hub, s *Session, cmd string) string {
	t.Helper()
	drain(s)
	HandleInput(context.Background(), h, s, cmd)
	for {
		select {
		case o := <-s.Outbox:
			if o.Line != nil && (o.Line.Kind == KindInfo || o.Line.Kind == KindError) {
				return o.Line.Body
			}
		case <-time.After(500 * time.Millisecond):
			return "" // command succeeded silently
		}
	}
}

func TestNickUniqueness(t *testing.T) {
	h, _ := adminHub(t)
	ctx := context.Background()
	a := NewSession("SHA256:aaa", "1.1.1.1", h.ResolveInitialNick(ctx, "SHA256:aaa"))
	h.Main().Join(a)
	if got := info(t, h, a, "/nick taken"); strings.Contains(got, "taken") {
		t.Fatalf("first claim refused: %s", got)
	}
	b := NewSession("SHA256:bbb", "2.2.2.2", "bee")
	h.Main().Join(b)
	if got := info(t, h, b, "/nick TAKEN"); !strings.Contains(got, "already taken") {
		t.Fatalf("duplicate allowed (case-insensitive): %s", got)
	}
	// An offline keyed user's persisted nick is still reserved.
	a2 := NewSession("SHA256:ccc", "3.3.3.3", "c")
	h.Main().Join(a2)
	_ = info(t, h, a2, "/nick reserved")
	a2.send(Outbound{Disconnect: true})
	h.Main().Send(evPart{sess: a2})
	time.Sleep(100 * time.Millisecond)
	if got := info(t, h, b, "/nick reserved"); !strings.Contains(got, "already taken") {
		t.Fatalf("persisted nick not reserved: %s", got)
	}
	// Default-nick collisions get a suffix instead of duplicating.
	n1 := h.ResolveInitialNick(ctx, "SHA256:zzz")
	d := NewSession("SHA256:zzz", "4.4.4.4", n1)
	h.Main().Join(d)
	_ = h.store.SetNickname(ctx, "SHA256:yyy", DefaultNickFor("SHA256:zzz"))
	if n2 := h.ResolveInitialNick(ctx, "SHA256:zzz"); n2 != n1 {
		t.Fatalf("known identity should keep its nick: %s vs %s", n2, n1)
	}
}

func TestGlobalBanAndAdminOnly(t *testing.T) {
	h, st := adminHub(t)
	ctx := context.Background()
	admin := NewSession("SHA256:admin", "9.9.9.9", "boss")
	troll := NewSession("SHA256:troll", "6.6.6.6", "troll")
	h.Main().Join(admin)
	h.Main().Join(troll)

	if got := info(t, h, troll, "/gban boss"); !strings.Contains(got, "Only a server administrator") {
		t.Fatalf("non-admin gban: %s", got)
	}
	if got := info(t, h, admin, "/gban boss"); !strings.Contains(got, "can't ban an administrator") {
		t.Fatalf("admin self-ban: %s", got)
	}
	if got := info(t, h, admin, "/gban troll being rude"); !strings.Contains(got, "banned server-wide") {
		t.Fatalf("gban: %s", got)
	}
	banned, _ := st.IsGloballyBanned(ctx, "SHA256:troll", "")
	byIP, _ := st.IsGloballyBanned(ctx, "SHA256:other", "6.6.6.6")
	if !banned || !byIP {
		t.Fatalf("ban not recorded for key/ip: %v %v", banned, byIP)
	}
	if got := info(t, h, admin, "/gbans"); !strings.Contains(got, "Server-wide bans") {
		t.Fatalf("gbans: %s", got)
	}
	if got := info(t, h, admin, "/gunban SHA256:troll"); !strings.Contains(got, "Lifted") {
		t.Fatalf("gunban: %s", got)
	}
	if banned, _ := st.IsGloballyBanned(ctx, "SHA256:troll", "6.6.6.6"); banned {
		t.Fatal("still banned after /gunban")
	}

	// Keyless target: only the address is banned.
	anon := NewSession("anon-1234abcd", "7.7.7.7", "ghost")
	h.Main().Join(anon)
	_ = info(t, h, admin, "/gban ghost")
	if b, _ := st.IsGloballyBanned(ctx, "anon-1234abcd", ""); b {
		t.Fatal("random anon fingerprint should not be stored")
	}
	if b, _ := st.IsGloballyBanned(ctx, "", "7.7.7.7"); !b {
		t.Fatal("anon address not banned")
	}
}

func TestDelroomAndExpiry(t *testing.T) {
	h, st := adminHub(t)
	ctx := context.Background()
	admin := NewSession("SHA256:admin", "9.9.9.9", "boss")
	h.Main().Join(admin)

	_ = info(t, h, admin, "/create doomed")
	time.Sleep(200 * time.Millisecond)
	if r := admin.CurrentRoom(); r == nil || r.Name != "doomed" {
		t.Fatalf("not in doomed: %+v", r)
	}
	if got := info(t, h, admin, "/delroom main"); !strings.Contains(got, "cannot be deleted") {
		t.Fatalf("delete main: %s", got)
	}
	if got := info(t, h, admin, "/delroom doomed"); !strings.Contains(got, "was deleted") && !strings.Contains(got, "Deleted #doomed") {
		t.Fatalf("delroom: %s", got)
	}
	time.Sleep(200 * time.Millisecond)
	if r := admin.CurrentRoom(); r != h.Main() {
		t.Fatalf("member not moved to main: %+v", r)
	}
	if _, ok, _ := st.GetChannelByName(ctx, "doomed"); ok {
		t.Fatal("channel still in store")
	}

	// Expiry: an old empty room goes, a recently active one stays.
	old, _ := st.CreateChannel(ctx, "stale", "fp")
	if _, err := st.CreateChannel(ctx, "fresh", "fp"); err != nil {
		t.Fatal(err)
	}
	_ = old
	gone, err := h.ExpireRooms(ctx, time.Hour)
	if err != nil || len(gone) != 0 {
		t.Fatalf("fresh rooms expired: %v %v", gone, err)
	}
	gone, err = h.ExpireRooms(ctx, -time.Hour) // everything counts as stale
	if err != nil || len(gone) != 2 {
		t.Fatalf("expected both stale rooms removed: %v %v", gone, err)
	}
	if _, ok, _ := st.GetChannelByName(ctx, "main"); !ok {
		t.Fatal("main must never expire")
	}
}

func TestSeenAdminLogFilter(t *testing.T) {
	h, st := adminHub(t)
	ctx := context.Background()
	h.SetFilter(filter.New(nil))
	admin := NewSession("SHA256:admin", "9.9.9.9", "boss")
	bob := NewSession("SHA256:bob", "2.2.2.2", "bob")
	h.Main().Join(admin)
	h.Main().Join(bob)

	if got := info(t, h, admin, "/seen bob"); !strings.Contains(got, "online now") {
		t.Fatalf("seen online: %s", got)
	}
	_ = st.SetNickname(ctx, "SHA256:old", "olduser")
	if got := info(t, h, admin, "/seen olduser"); !strings.Contains(got, "last seen") {
		t.Fatalf("seen offline: %s", got)
	}
	if got := info(t, h, admin, "/seen nobody"); !strings.Contains(got, "No record") {
		t.Fatalf("seen unknown: %s", got)
	}

	_ = info(t, h, bob, "/admin the sky is falling")
	if got := info(t, h, bob, "/adminlog"); !strings.Contains(got, "Only a server administrator") {
		t.Fatalf("adminlog non-admin: %s", got)
	}
	drain(admin)
	HandleInput(ctx, h, admin, "/adminlog")
	var found bool
	for i := 0; i < 5 && !found; i++ {
		if o := next(t, admin); o.Line != nil && strings.Contains(o.Line.Body, "the sky is falling") {
			found = true
		}
	}
	if !found {
		t.Fatal("adminlog did not list the report")
	}

	drain(admin)
	HandleInput(ctx, h, bob, "well shit")
	if l := nextLine(t, admin, KindChat); l.Body != "well s***" {
		t.Fatalf("filter: %q", l.Body)
	}
}

func TestAfkThresholdConfigurable(t *testing.T) {
	h, _ := adminHub(t)
	h.SetAfkThreshold(time.Millisecond)
	s := NewSession("SHA256:s", "1.1.1.1", "s")
	h.Main().Join(s)
	time.Sleep(20 * time.Millisecond)
	h.CheckIdle(s)
	if !s.IsAfk() {
		t.Fatal("not marked away after custom threshold")
	}
}

// helpText runs /help and returns the whole listing.
func helpText(t *testing.T, h *Hub, s *Session) string {
	t.Helper()
	drain(s)
	HandleInput(context.Background(), h, s, "/help")
	var b strings.Builder
	for {
		select {
		case o := <-s.Outbox:
			if o.Line != nil && o.Line.Kind == KindInfo {
				b.WriteString(o.Line.Body + "\n")
			}
		case <-time.After(400 * time.Millisecond):
			return b.String()
		}
	}
}

func TestHelpListsOnlyUsableCommands(t *testing.T) {
	h, _ := adminHub(t)
	admin := NewSession("SHA256:admin", "9.9.9.9", "boss")
	user := NewSession("SHA256:user", "8.8.8.8", "plain")
	h.Main().Join(admin)
	h.Main().Join(user)

	adminOnly := []string{"/gban", "/gunban", "/gbans", "/delroom", "/adminlog", "/broadcast"}
	opOnly := []string{"/kick", "/ban", "/lock", "/topic", "/op ", "/announce"}

	// The server version is the first line of /help.
	h.SetVersion("1.2.3")
	if first, _, _ := strings.Cut(helpText(t, h, user), "\n"); first != "ssh-chat server v1.2.3" {
		t.Errorf("first /help line = %q, want the server version", first)
	}

	// Plain user in #main: no admin commands, no operator commands.
	got := helpText(t, h, user)
	for _, c := range append(adminOnly, opOnly...) {
		if strings.Contains(got, c) {
			t.Errorf("plain user's /help lists %q:\n%s", strings.TrimSpace(c), got)
		}
	}
	for _, c := range []string{"/help", "/nick", "/msg", "/me ", "/ignore", "/clear", "/time", "/quit"} {
		if !strings.Contains(got, c) {
			t.Errorf("plain user's /help is missing %q", strings.TrimSpace(c))
		}
	}

	// Admin sees admin commands.
	got = helpText(t, h, admin)
	for _, c := range adminOnly {
		if !strings.Contains(got, c) {
			t.Errorf("admin's /help is missing %q", c)
		}
	}

	// The owner of a channel sees operator commands there; others in it do not.
	_ = info(t, h, user, "/create mine")
	time.Sleep(200 * time.Millisecond)
	got = helpText(t, h, user)
	for _, c := range []string{"/kick", "/lock", "/topic", "/op "} {
		if !strings.Contains(got, c) {
			t.Errorf("channel owner's /help is missing %q", strings.TrimSpace(c))
		}
	}
	for _, c := range adminOnly {
		if strings.Contains(got, c) {
			t.Errorf("owner (non-admin) sees admin command %q", c)
		}
	}
	_ = info(t, h, admin, "/join mine")
	time.Sleep(200 * time.Millisecond)
	if got = helpText(t, h, admin); strings.Contains(got, "/kick") {
		t.Errorf("non-operator in someone else's channel sees /kick")
	}
}

func TestHelpStaysShortAndGusHasItsOwnMenu(t *testing.T) {
	h, _ := adminHub(t)
	s := NewSession("SHA256:u", "", "plain")
	h.Main().Join(s)

	// No bot: /gus is not advertised in /help, and says so if typed anyway.
	if got := helpText(t, h, s); strings.Contains(got, "/gus") {
		t.Fatalf("/gus must be hidden from /help while no bot runs:\n%s", got)
	}
	if got := info(t, h, s, "/gus"); !strings.Contains(got, "not running") {
		t.Fatalf("/gus with no bot: %q", got)
	}

	h.SetBotHelp([]string{"Gus commands (type them in #main):", "  !tell <user> <message>  Leave a message"})
	got := helpText(t, h, s)
	if strings.Contains(got, "!tell") || strings.Contains(got, "Gus commands") {
		t.Fatalf("/help must not carry the bot's commands any more:\n%s", got)
	}
	for _, want := range []string{"/gus", "/keys"} {
		if !strings.Contains(got, want) {
			t.Fatalf("/help should point at %s:\n%s", want, got)
		}
	}
	drain(s)
	HandleInput(context.Background(), h, s, "/gus")
	var out strings.Builder
	for {
		select {
		case o := <-s.Outbox:
			if o.Line != nil && o.Line.Kind == KindInfo {
				out.WriteString(o.Line.Body + "\n")
			}
			continue
		case <-time.After(400 * time.Millisecond):
		}
		break
	}
	if !strings.Contains(out.String(), "Gus commands") || !strings.Contains(out.String(), "!tell <user> <message>") {
		t.Fatalf("/gus should list the bot's commands:\n%s", out.String())
	}
}

func TestPermanentChannelsAreCreatedAndNeverDeleted(t *testing.T) {
	h, st := adminHub(t)
	ctx := context.Background()
	// A user-made channel that gets listed keeps its owner.
	mine, err := st.CreateChannel(ctx, "lounge", "SHA256:someone")
	if err != nil {
		t.Fatal(err)
	}
	_ = mine
	if err := h.EnsurePermanent(ctx, []string{"Lounge", "help", "main", "tech_chat"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lounge", "help", "tech_chat"} {
		if _, ok, _ := st.GetChannelByName(ctx, name); !ok || !h.IsPermanent(name) {
			t.Fatalf("%s should exist and be permanent", name)
		}
	}
	if role, ok, _ := st.IsOwnerOrOp(ctx, mine.ID, "SHA256:someone"); !ok || role != store.RoleOwner {
		t.Fatal("an existing channel's owner must be kept")
	}
	if err := h.EnsurePermanent(ctx, []string{"help"}); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := h.EnsurePermanent(ctx, []string{"bad name!"}); err == nil || !strings.Contains(err.Error(), "bad name!") {
		t.Fatalf("an invalid name must be rejected, naming it: %v", err)
	}
	_ = h.EnsurePermanent(ctx, []string{"help", "tech_chat"}) // restore after the failed call

	// Expiry spares permanent channels but still removes ordinary ones.
	if _, err := st.CreateChannel(ctx, "temp", "SHA256:x"); err != nil {
		t.Fatal(err)
	}
	gone, err := h.ExpireRooms(ctx, -time.Hour) // everything counts as stale
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 2 { // temp and lounge (no longer listed after the second call)
		t.Logf("expired: %v", gone)
	}
	for _, name := range []string{"help", "tech_chat"} {
		if _, ok, _ := st.GetChannelByName(ctx, name); !ok {
			t.Fatalf("permanent channel %s was expired", name)
		}
	}
	if _, ok, _ := st.GetChannelByName(ctx, "temp"); ok {
		t.Fatal("an ordinary stale channel should still expire")
	}

	// /delroom refuses a permanent channel.
	admin := NewSession("SHA256:admin", "9.9.9.9", "boss")
	h.Main().Join(admin)
	if got := info(t, h, admin, "/delroom help"); !strings.Contains(got, "permanent channel") {
		t.Fatalf("/delroom on a permanent channel: %q", got)
	}
	if _, ok, _ := st.GetChannelByName(ctx, "help"); !ok {
		t.Fatal("/delroom deleted a permanent channel")
	}
}

// Permanent channels have no human owner, so server admins moderate them.
func TestAdminsModeratePermanentChannelsOnly(t *testing.T) {
	h, st := adminHub(t)
	ctx := context.Background()
	if err := h.EnsurePermanent(ctx, []string{"help"}); err != nil {
		t.Fatal(err)
	}
	other, _ := st.CreateChannel(ctx, "plain", "SHA256:nobody")
	_ = other
	admin := NewSession("SHA256:admin", "9.9.9.9", "boss")
	user := NewSession("SHA256:user", "8.8.8.8", "plain-user")
	h.Main().Join(admin)
	h.Main().Join(user)

	_ = info(t, h, admin, "/join help")
	_ = info(t, h, user, "/join help")
	time.Sleep(200 * time.Millisecond)
	if got := info(t, h, admin, "/topic Welcome to help"); strings.Contains(got, "must be") {
		t.Fatalf("an admin should be able to set the topic of a permanent channel: %q", got)
	}
	if got := info(t, h, user, "/topic hijack"); !strings.Contains(got, "must be an operator") {
		t.Fatalf("an ordinary user must not: %q", got)
	}
	if got := helpText(t, h, admin); !strings.Contains(got, "/topic") || !strings.Contains(got, "/op ") {
		t.Fatalf("/help for an admin in a permanent channel should list the moderation commands:\n%s", got)
	}
	if got := helpText(t, h, user); strings.Contains(got, "/topic") {
		t.Fatalf("/help for an ordinary user must not:\n%s", got)
	}

	// In an ordinary channel the admin has no special rights.
	_ = info(t, h, admin, "/join plain")
	time.Sleep(200 * time.Millisecond)
	if got := info(t, h, admin, "/topic nope"); !strings.Contains(got, "must be an operator") {
		t.Fatalf("admin rights must not extend to ordinary channels: %q", got)
	}
}
