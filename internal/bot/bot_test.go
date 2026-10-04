package bot

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type pmMsg struct{ to, text string }

type fakeHost struct {
	motds    []pmMsg             // boxed MOTD blocks shown to a user (to = nick)
	ignoring map[string]bool     // nicks that are ignoring the bot (their private messages are not delivered)
	admins   map[string]bool     // fingerprints the server treats as administrators
	sayIn    map[string][]string // lines spoken in channels other than #main
	said     []string
	actions  []string
	pms      []pmMsg
	online   []Person
}

func (f *fakeHost) Say(t string)              { f.said = append(f.said, t) }
func (f *fakeHost) SayIn(room, t string)      { f.sayIn[room] = append(f.sayIn[room], t) }
func (f *fakeHost) Action(t string)           { f.actions = append(f.actions, t) }
func (f *fakeHost) PM(n, t string)            { f.pms = append(f.pms, pmMsg{n, t}) }
func (f *fakeHost) Online() []Person          { return f.online }
func (f *fakeHost) OnlineCount() int          { return len(f.online) }
func (f *fakeHost) IsAdmin(fp string) bool    { return f.admins[fp] }
func (f *fakeHost) ShowMotd(n, t string) bool { f.motds = append(f.motds, pmMsg{n, t}); return true }
func (f *fakeHost) TryPM(n, t string) bool {
	if f.ignoring[n] {
		return false
	}
	f.pms = append(f.pms, pmMsg{n, t})
	return true
}
func (f *fakeHost) reset() {
	f.said, f.pms, f.actions, f.motds = nil, nil, nil, nil
	f.sayIn = map[string][]string{}
}
func (f *fakeHost) joined(p Person) { f.online = append(f.online, p) }
func (f *fakeHost) left(p Person) {
	for i, q := range f.online {
		if q.ID == p.ID {
			f.online = append(f.online[:i], f.online[i+1:]...)
			return
		}
	}
}

type env struct {
	dbPath string
	t      *testing.T
	b      *Bot
	h      *fakeHost
	clock  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, h: &fakeHost{sayIn: map[string][]string{}, admins: map[string]bool{}, ignoring: map[string]bool{}}, clock: time.Date(2026, 10, 7, 14, 0, 0, 0, time.UTC)} // a Wednesday afternoon
	e.dbPath = filepath.Join(t.TempDir(), "bot.db")
	b, err := New(e.h, Options{
		DBPath:        e.dbPath,
		UnmatchedPath: filepath.Join(t.TempDir(), "unmatched.log"),
		Now:           func() time.Time { return e.clock },
		Sleep:         func(time.Duration) {},
		Seed:          [2]uint64{1, 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	b.cfg.loc = time.UTC
	b.cfg.RemarkProbability = 0 // opt in per test
	e.b = b
	t.Cleanup(func() { _ = b.st.close() })
	return e
}

func keyed(id, nick string) Person { return Person{ID: id, Nick: nick, FP: "SHA256:" + id} }
func anonP(id, nick string) Person { return Person{ID: id, Nick: nick, FP: "anon-" + id, Anon: true} }

func (e *env) login(p Person)  { e.h.joined(p); e.b.Handle(Event{Kind: EvLogin, P: p}) }
func (e *env) logoff(p Person) { e.h.left(p); e.b.Handle(Event{Kind: EvLogoff, P: p}) }
func (e *env) chat(p Person, s string) {
	e.b.Handle(Event{Kind: EvChat, P: p, Body: s})
}
func (e *env) advance(d time.Duration) { e.clock = e.clock.Add(d) }

func (e *env) said() string { return strings.Join(e.h.said, "\n") }

// inPool reports whether line is some variant of pool with vars filled.
func (e *env) inPool(pool string, vars map[string]string, line string) bool {
	for _, v := range e.b.c.Pools[pool] {
		if fill(v.Text, vars) == line {
			return true
		}
	}
	return false
}

func TestStarterContentMeetsSpec(t *testing.T) {
	_, c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePools(c); err != nil {
		t.Fatal(err)
	}
	allowed := map[string][]string{
		"login": {"nick"}, "logoff": {"nick"}, "greet_first": {"nick", "board"}, "greet_returning": {"nick", "days"},
		"greet_anon": {"nick"}, "absence_30": {"nick", "days"}, "milestone_caller": {"nick", "n"},
	}
	ph := regexp.MustCompile(`\{(\w+)\}`)
	global := map[string]bool{"nick": true, "bot": true, "board": true, "days": true, "n": true, "years": true,
		"target": true, "from": true, "ago": true, "msg": true, "expr": true, "total": true, "rolls": true,
		"time": true, "count": true, "list": true, "date": true, "calls": true, "users": true, "record": true,
		"trivia": true, "seconds": true, "pts": true, "answer": true}
	// Pools that run without a person must not use {nick}.
	noNick := map[string]bool{"lonely": true, "idle_intro": true, "quote_intro": true, "maintenance": true,
		"trivia_ask": true, "trivia_timeout": true, "node_record": true}
	for name, pool := range c.Pools {
		if len(pool) < 5 || len(pool) > 10 {
			t.Errorf("pool %s has %d variants, want 5-10", name, len(pool))
		}
		for _, v := range pool {
			for _, m := range ph.FindAllStringSubmatch(v.Text, -1) {
				if !global[m[1]] {
					t.Errorf("pool %s: unknown placeholder {%s}", name, m[1])
				}
				if m[1] == "nick" && noNick[name] {
					t.Errorf("pool %s uses {nick} but runs with no person", name)
				}
			}
		}
	}
	_ = allowed
	for _, name := range []string{"greet_first", "greet_returning", "greet_returning_today", "greet_rapid", "greet_anon"} {
		if len(c.Pools[name]) < 6 {
			t.Errorf("greeting pool %s too small", name)
		}
	}
	for _, name := range []string{"greet_first", "greet_returning"} {
		if len(c.Pools[name]) < 8 {
			t.Errorf("%s needs at least 8 variants, has %d", name, len(c.Pools[name]))
		}
	}
	if len(c.Trivia) < 20 || len(c.Fortunes) < 20 || len(c.Oneliners) < 15 {
		t.Errorf("starter content too small: trivia=%d fortunes=%d oneliners=%d", len(c.Trivia), len(c.Fortunes), len(c.Oneliners))
	}
	for _, q := range c.Trivia {
		if q.Q == "" || len(q.A) == 0 {
			t.Errorf("bad trivia entry %+v", q)
		}
	}
	for _, h := range c.History {
		if _, err := time.Parse("01-02", h.Date); err != nil {
			t.Errorf("bad history date %q", h.Date)
		}
	}
}

func TestJoinAndLeave(t *testing.T) {
	e := newEnv(t)
	dave := keyed("dave", "Dave")
	e.login(dave)
	if len(e.h.said) != 1 || !e.inPool("greet_first", e.b.vars(dave), e.h.said[0]) {
		t.Fatalf("expected exactly one comment, a greet_first variant naming Dave, got %q", e.h.said)
	}

	e.h.reset()
	e.logoff(dave)
	if len(e.h.said) != 1 || !e.inPool("logoff", e.b.vars(dave), e.h.said[0]) {
		t.Fatalf("expected one logoff line, got %v", e.h.said)
	}

	// Returning after 5 days gets "N days".
	e.advance(5 * 24 * time.Hour)
	e.h.reset()
	e.login(dave)
	if len(e.h.said) != 1 || !e.inPool("greet_returning", e.b.vars(dave, "days", "5"), e.h.said[0]) {
		t.Fatalf("want exactly one greet_returning comment with 5 days, got %v", e.h.said)
	}

	// Quick redial gets the rapid-reconnect joke.
	e.logoff(dave)
	e.advance(30 * time.Second)
	e.h.reset()
	e.login(dave)
	if len(e.h.said) != 1 || !e.inPool("greet_rapid", e.b.vars(dave), e.h.said[0]) {
		t.Fatalf("want exactly one greet_rapid comment, got %v", e.h.said)
	}
}

func TestSilentDuringNormalChat(t *testing.T) {
	e := newEnv(t)
	a, b := keyed("a", "Alice"), keyed("b", "Bob")
	e.login(a)
	e.login(b)
	e.chat(a, "hi bob")
	e.chat(b, "hi alice")
	e.h.reset()
	for _, line := range []string{"how was your day", "mine was ok", "check this out https://example.com", "lol", "!notacommand", "who knows",
		"the sysop is annoying", "gus is cool"} {
		e.chat(a, line)
		e.chat(b, line+" 2")
		e.advance(5 * time.Second)
		e.b.Tick()
	}
	if len(e.h.said)+len(e.h.pms) != 0 {
		t.Fatalf("bot spoke during normal chat: %v %v", e.h.said, e.h.pms)
	}
}

func TestAddressedAndCommands(t *testing.T) {
	e := newEnv(t)
	a := keyed("a", "Alice")
	e.login(a)
	e.chat(a, "warmup") // burn first-message reaction
	e.h.reset()

	e.chat(a, "@SysOp-Gus asdf qwerty")
	if len(e.h.said) != 1 || !e.inPool("fallback", e.b.vars(a), e.h.said[0]) {
		t.Fatalf("want fallback, got %v", e.h.said)
	}
	data, _ := readFile(e.b.unmatchedPath)
	if !strings.Contains(data, "Alice") || !strings.Contains(data, "asdf qwerty") {
		t.Fatalf("unmatched.log missing entry: %q", data)
	}

	e.h.reset()
	e.chat(a, "@SysOp-Gus how do I change my nickname?")
	if !strings.Contains(e.said(), "/nick") {
		t.Fatalf("FAQ miss: %v", e.h.said)
	}

	for _, cmd := range []string{"!help", "!time", "!who", "!stats", "!rules", "!motd", "!fortune", "!quote", "!roll 2d6", "!ROLL d20", "!seen Alice"} {
		e.advance(20 * time.Second) // stay under the per-user rate limit
		e.h.reset()
		e.chat(a, cmd)
		if len(e.h.said)+len(e.h.motds)+len(e.h.pms) == 0 { // !motd answers privately, in a box
			t.Errorf("%s produced no reply", cmd)
		}
	}
	e.advance(20 * time.Second)
	e.h.reset()
	e.chat(a, "!roll 99d99")
	if !e.inPool("roll_bad", e.b.vars(a), e.said()) {
		t.Errorf("bad roll not rejected: %v", e.h.said)
	}
}

func TestIdleAndLonely(t *testing.T) {
	e := newEnv(t)
	a, b := keyed("a", "Alice"), keyed("b", "Bob")
	e.login(a)
	e.login(b)
	e.h.reset()

	e.advance(10 * time.Minute)
	e.b.Tick()
	if len(e.h.said) != 0 {
		t.Fatalf("posted too early: %v", e.h.said)
	}
	e.advance(20 * time.Minute) // 30 min quiet
	e.b.Tick()
	if len(e.h.said) < 2 {
		t.Fatalf("expected idle intro + content, got %v", e.h.said)
	}
	first := len(e.h.said)

	// Never twice in a row without human activity.
	e.advance(2 * time.Hour)
	e.b.Tick()
	e.b.Tick()
	if len(e.h.said) != first {
		t.Fatalf("idle posted twice without human activity: %v", e.h.said)
	}
	e.chat(a, "I'm back")
	e.advance(30 * time.Minute)
	e.h.reset()
	e.b.Tick()
	if len(e.h.said) == 0 {
		t.Fatal("idle should resume after human activity")
	}

	// Lonely: one user, quiet.
	e2 := newEnv(t)
	solo := keyed("s", "Solo")
	e2.login(solo)
	e2.h.reset()
	e2.advance(13 * time.Minute)
	e2.b.Tick()
	if len(e2.h.said) != 1 || !e2.inPool("lonely", e2.b.vars(Person{}), e2.h.said[0]) {
		t.Fatalf("want one lonely line, got %v", e2.h.said)
	}
	e2.advance(3 * time.Hour)
	e2.h.reset()
	e2.b.Tick()
	if len(e2.h.said) != 0 {
		t.Fatalf("lonely/idle repeated without human activity: %v", e2.h.said)
	}

	// Nobody online: stay quiet.
	e3 := newEnv(t)
	e3.advance(5 * time.Hour)
	e3.b.Tick()
	if len(e3.h.said) != 0 {
		t.Fatalf("spoke to an empty room: %v", e3.h.said)
	}
}

func TestBusyRoomStaysQuiet(t *testing.T) {
	e := newEnv(t)
	e.b.cfg.BusyGreetProbability = 0
	for i := 0; i < 6; i++ {
		e.h.joined(keyed(string(rune('a'+i)), "U"+string(rune('a'+i))))
	}
	newbie := keyed("z", "Zed")
	e.h.reset()
	e.login(newbie)
	if len(e.h.said) != 0 {
		t.Fatalf("busy room got chatty: %v", e.h.said)
	}
	e.advance(3 * time.Hour)
	e.b.Tick()
	if len(e.h.said) != 0 {
		t.Fatalf("idle post in a busy room: %v", e.h.said)
	}
}

func TestTellDeliveredAtNextLogin(t *testing.T) {
	e := newEnv(t)
	alice, bob := keyed("a", "Alice"), keyed("b", "Bob")
	e.login(bob)
	e.logoff(bob)
	e.login(alice)
	e.chat(alice, "hi all") // spend the first-message reaction
	e.h.reset()

	e.chat(alice, "!tell Bob the board is on fire  \x1b[31mred\x1b[0m")
	tv := e.b.vars(alice, "target", "Bob")
	if len(e.h.said) != 1 || !e.inPool("tell_stored", tv, e.h.said[0]) {
		t.Fatalf("want tell_stored, got %v", e.h.said)
	}
	e.advance(26 * time.Hour)
	e.h.reset()
	e.login(bob)
	if pms := e.h.pmsExceptMotd(); len(pms) != 1 || pms[0].to != "Bob" {
		t.Fatalf("want one (non-MOTD) PM to Bob, got %v", e.h.pms)
	}
	body := e.h.pmsExceptMotd()[0].text
	if !strings.Contains(body, "board is on fire") || !strings.Contains(body, "Alice") || strings.ContainsRune(body, 0x1b) {
		t.Fatalf("bad delivery (must be sanitised): %q", body)
	}
	// Delivered once only.
	e.logoff(bob)
	e.h.reset()
	e.login(bob)
	if len(e.h.pmsExceptMotd()) != 0 {
		t.Fatalf("tell delivered twice: %v", e.h.pms)
	}
}

func TestTellRules(t *testing.T) {
	e := newEnv(t)
	alice, bob, ghost := keyed("a", "Alice"), keyed("b", "Bob"), anonP("g", "Ghost")
	e.login(bob)
	e.logoff(bob)
	e.login(alice)
	e.login(ghost)
	e.chat(alice, "hi all") // spend the first-message reaction
	e.h.reset()

	check := func(from Person, cmd, pool string) {
		t.Helper()
		e.advance(time.Minute)
		e.h.reset()
		e.chat(from, cmd)
		if len(e.h.said) != 1 {
			t.Fatalf("%q: want one reply, got %v", cmd, e.h.said)
		}
		for _, v := range e.b.c.Pools[pool] {
			if fill(v.Text, e.b.vars(from, "target", strings.Fields(cmd + " x")[1])) == e.h.said[0] {
				return
			}
		}
		t.Fatalf("%q: reply %q is not from pool %s", cmd, e.h.said[0], pool)
	}
	// The keyless advice is a private message, never public chat.
	e.advance(time.Minute)
	e.h.reset()
	e.chat(ghost, "!tell Bob hi")
	if len(e.h.said) != 0 || len(e.h.pms) != 1 || e.h.pms[0].to != "Ghost" ||
		!e.inPool("tell_need_key", e.b.vars(ghost), e.h.pms[0].text) {
		t.Fatalf("tell_need_key must be a PM to Ghost only: said=%v pms=%v", e.h.said, e.h.pms)
	}
	check(alice, "!tell Nobody hi", "tell_unknown")
	check(alice, "!tell Alice hi", "tell_self")
	check(alice, "!tell SysOp-Gus hi", "tell_self")
	check(alice, "!tell Bob", "tell_usage")
	check(alice, "!tell Ghost hi", "tell_unknown") // anon target online
	e.login(bob)
	check(alice, "!tell Bob you're here", "tell_online")
	e.logoff(bob)

	for i := 0; i < e.b.cfg.TellMaxPerSender; i++ {
		check(alice, "!tell Bob msg", "tell_stored")
	}
	check(alice, "!tell Bob one more", "tell_limit")

	// Recipient cap: other senders hit tell_full.
	e.b.cfg.TellMaxPerRecipient = e.b.cfg.TellMaxPerSender
	carol := keyed("c", "Carol")
	e.login(carol)
	e.chat(carol, "hi all")
	check(carol, "!tell Bob overflow", "tell_full")

	// Expiry.
	e.b.st.purgeTells(e.clock.Add(time.Hour))
	if n := e.b.st.countTellsTo("SHA256:b"); n != 0 {
		t.Fatalf("expired tells not purged: %d", n)
	}
}

func TestEchoedTextCannotBecomeCommands(t *testing.T) {
	e := newEnv(t)
	e.b.deliver([]outLine{{text: "/gban everyone"}, {text: "/quit now"}, {text: "ok"}})
	for _, l := range e.h.said {
		if strings.HasPrefix(l, "/") {
			t.Fatalf("line could run as a server command: %q", l)
		}
	}
	if clean("a\x1b[2Jb\r\nc\x00d", 0) != "a [2Jb c d" {
		t.Fatalf("clean() = %q", clean("a\x1b[2Jb\r\nc\x00d", 0))
	}
	if got := clean(strings.Repeat("x", 500), 10); len(got) != 10 {
		t.Fatalf("clean max: %d", len(got))
	}
}

func TestRateLimiting(t *testing.T) {
	e := newEnv(t)
	a := keyed("a", "Alice")
	e.login(a)
	e.chat(a, "x")
	e.h.reset()
	for i := 0; i < 30; i++ {
		e.chat(a, "!time")
	}
	replies := 0
	for _, l := range e.h.said {
		if !e.inPool("rate_limited", e.b.vars(a), l) {
			replies++
		}
	}
	if replies > e.b.cfg.RatePerUserPerMin {
		t.Fatalf("user got %d replies in a minute, limit %d", replies, e.b.cfg.RatePerUserPerMin)
	}
	if len(e.h.said) > e.b.cfg.RatePerUserPerMin+1 {
		t.Fatalf("rate-limit notice repeated: %d lines", len(e.h.said))
	}

	// The global cap bounds total output however many users ask.
	e2 := newEnv(t)
	for i := 0; i < 20; i++ {
		u := keyed(string(rune('a'+i)), "U"+string(rune('a'+i)))
		e2.h.joined(u)
		e2.chat(u, "x")
	}
	e2.h.reset()
	for i := 0; i < 20; i++ {
		e2.chat(e2.h.online[i], "!time")
		e2.chat(e2.h.online[i], "!who")
	}
	if len(e2.h.said) > e2.b.cfg.RateGlobalPerMin {
		t.Fatalf("global cap exceeded: %d lines", len(e2.h.said))
	}
}

func TestMilestonesAbsenceAnniversary(t *testing.T) {
	e := newEnv(t)
	e.b.st.set("calls", "99")
	dave := keyed("d", "Dave")
	e.login(dave)
	if !strings.Contains(e.said(), "100") {
		t.Fatalf("caller #100 not announced: %v", e.h.said)
	}

	// Visit milestone: pre-seed a user with 9 visits.
	e.b.st.recordVisit("SHA256:v", "Vera", e.clock)
	for i := 0; i < 8; i++ {
		e.b.st.recordVisit("SHA256:v", "Vera", e.clock)
	}
	e.advance(2 * time.Hour)
	e.h.reset()
	e.login(keyed("v", "Vera"))
	if !strings.Contains(e.said(), "10") {
		t.Fatalf("10th visit not announced: %v", e.h.said)
	}

	// Anniversary and a long absence together: still ONE comment, the
	// anniversary (higher priority). It is only marked announced once used.
	e.advance(24 * time.Hour)
	e.logoff(dave)
	e.advance(400 * 24 * time.Hour)
	e.h.reset()
	e.login(dave)
	if len(e.h.said) != 1 || !e.inPool("anniversary", e.b.vars(dave, "years", "1"), e.h.said[0]) {
		t.Fatalf("want exactly one comment, the first anniversary: %v", e.h.said)
	}
	e.logoff(dave)
	e.h.reset()
	e.advance(5 * time.Minute)
	e.login(dave)
	if strings.Contains(e.said(), "year(s)") {
		t.Fatalf("anniversary repeated: %v", e.h.said)
	}

	// A long absence on its own is called out, with the number of days.
	zed := keyed("z", "Zed")
	e.login(zed)
	e.logoff(zed)
	e.advance(40 * 24 * time.Hour)
	e.h.reset()
	e.login(zed)
	if len(e.h.said) != 1 || !e.inPool("absence_30", e.b.vars(zed, "days", "40"), e.h.said[0]) {
		t.Fatalf("want one absence_30 comment naming 40 days: %v", e.h.said)
	}
}

func TestTimeOfDayAndNodeRecord(t *testing.T) {
	e := newEnv(t)
	e.b.cfg.RemarkProbability = 1
	// A returning caller (not a first-timer) can get a time-of-day remark
	// INSTEAD of the plain "welcome back".
	e.clock = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	p := keyed("p", "Pat")
	e.login(p)
	e.logoff(p)
	e.clock = time.Date(2026, 10, 7, 2, 30, 0, 0, time.UTC) // 02:30 Wednesday
	e.h.reset()
	e.login(p)
	if len(e.h.said) != 1 || !e.inPool("remark_late", e.b.vars(p), e.h.said[0]) {
		t.Fatalf("want exactly one late-night remark: %v", e.h.said)
	}

	q := keyed("q", "Quin")
	e.clock = time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)
	e.login(q)
	e.logoff(q)
	e.clock = time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC) // Saturday
	e.h.reset()
	e.login(q)
	if len(e.h.said) != 1 || !e.inPool("remark_weekend", e.b.vars(q), e.h.said[0]) {
		t.Fatalf("want exactly one weekend remark: %v", e.h.said)
	}

	// Node record needs a previous record to beat; it is the single comment.
	e.b.cfg.RemarkProbability = 0
	for i := 0; i < 3; i++ {
		e.h.reset()
		e.login(keyed(string(rune('r'+i)), "R"+string(rune('0'+i))))
		if len(e.h.said) != 1 {
			t.Fatalf("login %d: want exactly one comment, got %v", i, e.h.said)
		}
	}
	if !e.inPool("node_record", e.b.vars(Person{}, "n", "5"), e.h.said[0]) && !strings.Contains(e.h.said[0], "5") {
		t.Fatalf("no node-count record announced on the busiest login: %v", e.h.said)
	}
}

// However many things are notable about a login, Gus says at most ONE
// public thing about it.
func TestAtMostOneCommentPerLogin(t *testing.T) {
	e := newEnv(t)
	e.b.cfg.RemarkProbability = 1
	e.b.cfg.BusyGreetProbability = 1
	e.b.st.set("calls", "99") // the next login is caller #100
	people := []Person{keyed("a", "Alice"), keyed("b", "Bob"), anonP("c", "Cara"), keyed("d", "Dan")}
	for round := 0; round < 40; round++ {
		for _, p := range people {
			e.h.reset()
			e.login(p)
			if len(e.h.said) > 1 {
				t.Fatalf("round %d: %s's login got %d public comments: %v", round, p.Nick, len(e.h.said), e.h.said)
			}
			e.logoff(p)
		}
		e.advance(time.Duration(round%5+1) * 26 * time.Hour * time.Duration(1+round%3*50))
	}
}

func TestFirstMessageReaction(t *testing.T) {
	e := newEnv(t)
	a := keyed("a", "Alice")
	e.login(a)
	e.h.reset()
	e.chat(a, "hello world")
	if len(e.h.said) != 1 || !e.inPool("first_message", e.b.vars(a), e.h.said[0]) {
		t.Fatalf("want first_message reaction, got %v", e.h.said)
	}
	e.h.reset()
	e.chat(a, "second message")
	if len(e.h.said) != 0 {
		t.Fatalf("reacted to a non-first message: %v", e.h.said)
	}
}

func TestScheduledPosts(t *testing.T) {
	e := newEnv(t)
	e.clock = time.Date(2026, 10, 8, 9, 0, 5, 0, time.UTC)
	e.b.Tick()
	if len(e.h.said) != 2 || !strings.Contains(e.h.said[1], "--") {
		t.Fatalf("daily quote missing: %v", e.h.said)
	}
	e.h.reset()
	e.advance(10 * time.Minute)
	e.b.Tick()
	if len(e.h.said) != 0 {
		t.Fatalf("daily quote repeated: %v", e.h.said)
	}
	e.clock = time.Date(2026, 10, 9, 3, 0, 5, 0, time.UTC)
	e.b.Tick()
	if len(e.h.said) != 1 || !e.inPool("maintenance", e.b.vars(Person{}), e.h.said[0]) {
		t.Fatalf("maintenance message missing: %v", e.h.said)
	}
	// Restarted late in the day: don't post a stale schedule.
	e.h.reset()
	e.clock = time.Date(2026, 10, 10, 18, 0, 0, 0, time.UTC)
	e.b.Tick()
	if len(e.h.said) != 0 {
		t.Fatalf("stale scheduled post: %v", e.h.said)
	}
}

func TestTriviaScoring(t *testing.T) {
	e := newEnv(t)
	a, b := keyed("a", "Alice"), keyed("b", "Bob")
	e.login(a)
	e.login(b)
	e.chat(a, "x")
	e.chat(b, "y")
	e.h.reset()

	e.chat(a, "!trivia")
	if e.b.trivia["main"] == nil || !strings.Contains(e.said(), "Q: ") {
		t.Fatalf("trivia question not asked: %v", e.h.said)
	}
	answer := e.b.trivia["main"].q.A[0]
	e.advance(10 * time.Second)
	e.h.reset()
	e.chat(b, "!trivia definitely wrong")
	if len(e.h.said) != 1 {
		t.Fatalf("wrong answer should get one reply: %v", e.h.said)
	}
	e.advance(5 * time.Second)
	e.h.reset()
	e.chat(a, "!trivia  The "+strings.ToUpper(answer)+"!")
	if e.b.trivia["main"] != nil || !strings.Contains(e.said(), "Alice") {
		t.Fatalf("correct answer not accepted: %v", e.h.said)
	}
	top := e.b.st.topScores(5)
	if len(top) != 1 || top[0].Nick != "Alice" || top[0].Points != 2 {
		t.Fatalf("scoring wrong (15s => 2 pts): %+v", top)
	}
	e.advance(time.Minute)
	e.h.reset()
	e.chat(b, "!trivia top")
	if !strings.Contains(e.said(), "1. Alice - 2 pts") {
		t.Fatalf("leaderboard wrong: %v", e.h.said)
	}

	// Timeout reveals the answer; second question while one is live is refused.
	e.advance(time.Minute)
	e.chat(a, "!trivia")
	e.advance(time.Minute)
	e.chat(b, "!trivia")
	if e.b.trivia["main"] == nil || !strings.Contains(e.said(), "Q:") {
		t.Fatalf("expected the live question re-announced: %v", e.h.said)
	}
	e.advance(61 * time.Second)
	e.h.reset()
	e.b.Tick()
	if e.b.trivia["main"] != nil || len(e.h.said) != 1 {
		t.Fatalf("timeout did not reveal the answer: %v", e.h.said)
	}
}

func TestForgetAndRemember(t *testing.T) {
	e := newEnv(t)
	a, b := keyed("a", "Alice"), keyed("b", "Bob")
	e.login(a)
	e.login(b)
	e.chat(a, "!trivia")
	e.chat(a, "!trivia "+e.b.trivia["main"].q.A[0])
	e.advance(time.Minute)
	e.chat(b, "!tell Alice hi") // online -> not queued; fine
	e.advance(time.Minute)
	e.h.reset()
	e.chat(a, "!forget")
	if _, ok := e.b.st.user("SHA256:a"); ok || len(e.b.st.topScores(5)) != 0 {
		t.Fatal("forget did not erase user data")
	}
	e.logoff(a)
	e.h.reset()
	e.login(a)
	if len(e.h.said) != 1 { // entrance only, nothing tracked
		t.Fatalf("opted-out user should only get the entrance line: %v", e.h.said)
	}
	if _, ok := e.b.st.user("SHA256:a"); ok {
		t.Fatal("opted-out user was tracked again")
	}
	e.advance(time.Minute)
	e.chat(a, "!remember")
	e.logoff(a)
	e.login(a)
	if _, ok := e.b.st.user("SHA256:a"); !ok {
		t.Fatal("!remember did not resume tracking")
	}
	// Anonymous callers: nothing to forget.
	g := anonP("g", "Ghost")
	e.login(g)
	e.advance(time.Minute)
	e.h.reset()
	e.chat(g, "!forget")
	if len(e.h.said) != 0 || len(e.h.pms) != 1 || !e.inPool("forget_anon", e.b.vars(g), e.h.pms[0].text) {
		t.Fatalf("anon forget reply must be a private message: said=%v pms=%v", e.h.said, e.h.pms)
	}
}

func TestSeenAndWho(t *testing.T) {
	e := newEnv(t)
	a, b := keyed("a", "Alice"), keyed("b", "Bob")
	e.login(b)
	e.logoff(b)
	e.advance(3 * time.Hour)
	e.login(a)
	e.chat(a, "x")
	e.h.reset()
	e.chat(a, "!seen bob")
	if !strings.Contains(e.said(), "Bob") || !strings.Contains(e.said(), "3h ago") {
		t.Fatalf("seen offline wrong: %v", e.h.said)
	}
	e.advance(time.Minute)
	e.h.reset()
	e.chat(a, "!seen Alice")
	if !e.inPool("seen_online", e.b.vars(a, "target", "Alice"), e.said()) {
		t.Fatalf("seen online wrong: %v", e.h.said)
	}
	e.advance(time.Minute)
	e.h.reset()
	e.chat(a, "!seen nobody")
	if !e.inPool("seen_unknown", e.b.vars(a, "target", "nobody"), e.said()) {
		t.Fatalf("seen unknown wrong: %v", e.h.said)
	}
}

func TestOutputOneMessagePerReplyAndPaces(t *testing.T) {
	e := newEnv(t)
	var slept []time.Duration
	e.b.sleep = func(d time.Duration) { slept = append(slept, d) }
	e.b.cfg.TypingIndicator = true
	e.b.cfg.Baud = 2400

	// A normal sentence is ONE chat message, however long: the chat screen
	// wraps it to the terminal. (Pre-wrapping it here used to split a
	// sentence across several messages, each with its own timestamp.)
	sentence := "Another caller: lucky-wombat. The modem pool was getting lonely."
	e.b.say(kindPlain, sentence)
	if len(e.h.said) != 1 || e.h.said[0] != sentence {
		t.Fatalf("a sentence must be sent as a single message, got %q", e.h.said)
	}

	// Only text longer than the server's message limit is split, at word
	// boundaries, with nothing lost.
	e.h.reset()
	long := strings.TrimSpace(strings.Repeat("word ", 300))
	e.b.say(kindPlain, long)
	if len(e.h.said) < 3 {
		t.Fatalf("over-long text should be split into several messages: %d", len(e.h.said))
	}
	for _, l := range e.h.said {
		if len(l) > e.b.cfg.MaxMessageChars {
			t.Fatalf("message of %d chars exceeds the %d limit", len(l), e.b.cfg.MaxMessageChars)
		}
	}
	if strings.Join(e.h.said, " ") != long {
		t.Fatal("splitting changed the text")
	}
	if len(e.h.actions) == 0 || !strings.Contains(e.h.actions[0], "typing") {
		t.Fatalf("typing indicator missing: %v", e.h.actions)
	}
	if len(slept) < len(e.h.said)+1 {
		t.Fatalf("typing delay and baud pacing not applied: %d sleeps for %d messages", len(slept), len(e.h.said))
	}
	// Disabled: no delay at all.
	e2 := newEnv(t)
	e2.b.cfg.TypingDelayMs = [2]int{0, 0}
	n := 0
	e2.b.sleep = func(time.Duration) { n++ }
	e2.b.say(kindPlain, "hi")
	if n != 0 || len(e2.h.actions) != 0 {
		t.Fatalf("delay not disabled: sleeps=%d actions=%v", n, e2.h.actions)
	}
}

func TestColourHelperSwitchesOff(t *testing.T) {
	if colourise(false, kindEvent, "x") != "x" {
		t.Fatal("colour off must be plain")
	}
	if !strings.Contains(colourise(true, kindEvent, "x"), "\x1b[") {
		t.Fatal("colour on must emit ANSI")
	}
	e := newEnv(t)
	e.b.say(kindEvent, "plain")
	for _, l := range e.h.said {
		if strings.ContainsRune(l, 0x1b) {
			t.Fatalf("default output must be plain text: %q", l)
		}
	}
}

func TestNoRepeatWithinRecentMemory(t *testing.T) {
	e := newEnv(t)
	last := []string{}
	for i := 0; i < 60; i++ {
		s := e.b.text("login", e.b.vars(keyed("a", "A")))
		for _, prev := range last {
			if prev == s {
				t.Fatalf("variant repeated within recent memory: %q", s)
			}
		}
		last = append(last, s)
		if len(last) > e.b.cfg.RecentMemory {
			last = last[1:]
		}
	}
}

func readFile(p string) (string, error) {
	b, err := osReadFile(p)
	return string(b), err
}

var osReadFile = os.ReadFile

func TestKeylessAdviceIsPrivate(t *testing.T) {
	e := newEnv(t)
	alice := keyed("a", "Alice")
	e.login(alice)
	e.h.reset()

	ghost := anonP("g", "Ghost")
	e.login(ghost)

	// Everyone in the room sees only the entrance line...
	if len(e.h.said) != 1 || !e.inPool("login", e.b.vars(ghost), e.h.said[0]) {
		t.Fatalf("public chat should only get the entrance line: %v", e.h.said)
	}
	for _, line := range e.h.said {
		for _, v := range e.b.c.Pools["greet_anon"] {
			if line == fill(v.Text, e.b.vars(ghost)) {
				t.Fatalf("keyless advice leaked into public chat: %q", line)
			}
		}
	}
	// ...and the advice goes to the keyless user alone.
	if pms := e.h.pmsExceptMotd(); len(pms) != 1 || pms[0].to != "Ghost" || !e.inPool("greet_anon", e.b.vars(ghost), pms[0].text) {
		t.Fatalf("want one greet_anon PM to Ghost (besides the MOTD), got %v", e.h.pms)
	}

	// Trivia: the "no score without a key" note is private; the answer is public.
	e.chat(ghost, "x")
	e.advance(time.Minute)
	e.chat(ghost, "!trivia")
	e.advance(5 * time.Second)
	e.h.reset()
	e.chat(ghost, "!trivia "+e.b.trivia["main"].q.A[0])
	if len(e.h.pms) != 1 || e.h.pms[0].to != "Ghost" || !e.inPool("trivia_anon", e.b.vars(ghost), e.h.pms[0].text) {
		t.Fatalf("trivia_anon must be a PM to Ghost: said=%v pms=%v", e.h.said, e.h.pms)
	}
	if len(e.h.said) == 0 {
		t.Fatal("the correct-answer announcement should still be public")
	}

	// A keyed user never gets keyless advice.
	e.h.reset()
	e.logoff(alice)
	e.login(alice)
	for _, m := range e.h.pms {
		if e.inPool("greet_anon", e.b.vars(alice), m.text) {
			t.Fatal("keyed user received keyless advice")
		}
	}
}

// /help and !help list the same commands, and every command is documented.
func TestCommandHelpMatchesTheHandlers(t *testing.T) {
	e := newEnv(t)
	handlers := e.b.handlers()
	seen := map[string]bool{}
	for _, c := range commandHelp {
		seen[c.name] = true
		if _, ok := handlers[c.name]; !ok {
			t.Errorf("commandHelp documents !%s but there is no handler", c.name)
		}
		if !strings.HasPrefix(c.usage, "!"+c.name) || c.help == "" {
			t.Errorf("bad help entry %+v", c)
		}
	}
	for name := range handlers {
		if !seen[name] {
			t.Errorf("!%s has a handler but is missing from commandHelp (so from /help)", name)
		}
	}
	lines := HelpLines("SysOp-Gus")
	if !strings.Contains(lines[0], "SysOp-Gus commands") || !strings.Contains(strings.Join(lines, "\n"), "!tell <user> <message>") {
		t.Fatalf("server help section: %q", lines)
	}
}

func (e *env) chatIn(room string, p Person, s string) {
	e.b.Handle(Event{Kind: EvChat, P: p, Body: s, Room: room})
}

// Gus's !commands and being addressed work in every channel, answering in the
// channel they were asked in and never leaking into #main.
func TestCommandsWorkInEveryChannel(t *testing.T) {
	e := newEnv(t)
	alice := keyed("a", "Alice")
	e.login(alice)
	e.chat(alice, "warmup") // spend the #main first-message reaction
	e.h.reset()

	e.chatIn("lounge", alice, "!time")
	if len(e.h.said) != 0 || len(e.h.sayIn["lounge"]) != 1 {
		t.Fatalf("!time in #lounge: main=%v lounge=%v", e.h.said, e.h.sayIn)
	}

	e.advance(time.Minute)
	e.h.reset()
	e.chatIn("lounge", alice, "@SysOp-Gus asdf qwerty")
	if len(e.h.sayIn["lounge"]) != 1 || len(e.h.said) != 0 || !e.inPool("fallback", e.b.vars(alice), e.h.sayIn["lounge"][0]) {
		t.Fatalf("addressed in #lounge: %v main=%v", e.h.sayIn, e.h.said)
	}

	// Ordinary chat in another channel is none of his business.
	e.advance(time.Minute)
	e.h.reset()
	for _, line := range []string{"hello everyone", "has anyone seen the footy", "lol"} {
		e.chatIn("lounge", alice, line)
	}
	if len(e.h.said)+len(e.h.sayIn["lounge"])+len(e.h.pms) != 0 {
		t.Fatalf("Gus spoke during normal chat in #lounge: %v %v", e.h.said, e.h.sayIn)
	}

	// Chatting in another channel is not #main activity: no first-message
	// reaction there, and it does not reset the idle timer.
	bob := keyed("b", "Bob")
	e.login(bob)
	e.h.reset()
	e.chatIn("lounge", bob, "my very first message")
	if len(e.h.said)+len(e.h.sayIn["lounge"]) != 0 {
		t.Fatalf("a first message in #lounge must not trigger the #main reaction: %v %v", e.h.said, e.h.sayIn)
	}
	if u, _ := e.b.st.user("SHA256:b"); u.Messages != 0 {
		t.Fatal("messages in other channels must not be counted as #main activity")
	}

	// !tell still queues mail from a side channel.
	carol := keyed("c", "Carol")
	e.login(carol)
	e.logoff(carol)
	e.advance(time.Minute)
	e.h.reset()
	e.chatIn("lounge", alice, "!tell Carol hello from the lounge")
	if len(e.h.sayIn["lounge"]) != 1 || !e.inPool("tell_stored", e.b.vars(alice, "target", "Carol"), e.h.sayIn["lounge"][0]) {
		t.Fatalf("!tell in #lounge: %v", e.h.sayIn)
	}
}

// Trivia is per channel: a question asked in one is not answered, scored or
// revealed in another.
func TestTriviaIsPerChannel(t *testing.T) {
	e := newEnv(t)
	a := keyed("a", "Alice")
	e.login(a)
	e.chat(a, "x")
	e.h.reset()

	e.chatIn("lounge", a, "!trivia")
	q := e.b.trivia["lounge"]
	if q == nil || e.b.trivia["main"] != nil || len(e.h.said) != 0 {
		t.Fatalf("question should live in #lounge only: lounge=%v main=%v said=%v", q, e.b.trivia["main"], e.h.said)
	}
	answer := q.q.A[0]

	e.advance(5 * time.Second)
	e.h.reset()
	e.chat(a, "!trivia "+answer) // the right answer, but in #main
	if !e.inPool("trivia_none", e.b.vars(a), e.said()) || e.b.trivia["lounge"] == nil {
		t.Fatalf("an answer in #main must not settle #lounge's question: %v", e.h.said)
	}

	e.advance(time.Minute)
	e.h.reset()
	e.chatIn("lounge", a, "!trivia "+answer)
	if e.b.trivia["lounge"] != nil || len(e.h.sayIn["lounge"]) == 0 || len(e.h.said) != 0 {
		t.Fatalf("answer in #lounge: lounge=%v main=%v", e.h.sayIn, e.h.said)
	}
	if top := e.b.st.topScores(5); len(top) != 1 || top[0].Nick != "Alice" {
		t.Fatalf("score not recorded: %+v", top)
	}

	// Timeout reveals in the channel it was asked in.
	e.advance(time.Minute)
	e.chatIn("lounge", a, "!trivia")
	e.advance(61 * time.Second)
	e.h.reset()
	e.b.Tick()
	if len(e.h.sayIn["lounge"]) != 1 || len(e.h.said) != 0 || e.b.trivia["lounge"] != nil {
		t.Fatalf("timeout reveal should be in #lounge: %v main=%v", e.h.sayIn, e.h.said)
	}
}

// pmsExceptMotd is the private messages minus the login MOTD, for tests about
// other kinds of private message.
// pmsExceptMotd is kept for the tests written when the MOTD was a PM; the MOTD
// is now a boxed block of its own (see motds), so this is simply the PMs.
func (f *fakeHost) pmsExceptMotd() []pmMsg { return f.pms }

// motdPMs is the MOTD texts shown (as boxes) to nick.
func (e *env) motdPMs(nick string) []string {
	var out []string
	for _, m := range e.h.motds {
		if m.to == nick {
			out = append(out, m.text)
		}
	}
	return out
}

func TestMotdShownPrivatelyAtEveryLogin(t *testing.T) {
	e := newEnv(t)
	// The data-file default is shown until an admin changes it - to keyed,
	// keyless and everyone alike, and only to the person logging in.
	for _, p := range []Person{keyed("a", "Alice"), anonP("g", "Ghost")} {
		e.h.reset()
		e.login(p)
		if got := e.motdPMs(p.Nick); len(got) != 1 || got[0] != e.b.c.Motd {
			t.Fatalf("%s should get the default MOTD privately: %v", p.Nick, e.h.pms)
		}
		for _, l := range e.h.said {
			if strings.Contains(l, e.b.c.Motd) {
				t.Fatal("the MOTD must not be broadcast to the room")
			}
		}
	}
	// Opted-out (untracked) users get it too.
	e.b.st.forget("SHA256:o")
	o := keyed("o", "Olly")
	e.h.reset()
	e.login(o)
	if len(e.motdPMs("Olly")) != 1 {
		t.Fatalf("an opted-out user should still get the MOTD: %v", e.h.pms)
	}
}

func TestOnlyAdminsCanSetTheMotdAndItTakesEffectImmediately(t *testing.T) {
	e := newEnv(t)
	admin, user, ghost := keyed("boss", "Boss"), keyed("u", "User"), anonP("g", "Ghost")
	e.h.admins["SHA256:boss"] = true
	e.h.admins["anon-g"] = true // even if a keyless identity were somehow listed, it is refused
	for _, p := range []Person{admin, user, ghost} {
		e.login(p)
		e.chat(p, "x") // spend the first-message reaction
	}
	say := func(p Person, text string) string {
		e.advance(time.Minute)
		e.h.reset()
		e.chat(p, text)
		return e.said()
	}

	// A non-admin cannot, however the command is phrased.
	for _, who := range []Person{user, ghost} {
		got := say(who, "!motd set Free beer for everyone")
		if !e.inPool("motd_denied", e.b.vars(who), got) {
			t.Fatalf("%s should be refused: %q", who.Nick, got)
		}
	}
	if got := say(user, "@SysOp-Gus !MOTD   CLEAR"); !e.inPool("motd_denied", e.b.vars(user), got) {
		t.Fatalf("clear by a non-admin: %q", got)
	}
	if e.b.currentMotd() != e.b.c.Motd {
		t.Fatal("the MOTD changed although only non-admins tried")
	}

	// An admin can; it applies at once, to !motd and to the next login.
	if got := say(admin, "!motd set Maintenance tonight at 9 \x1b[31mpm\x1b[0m"); !e.inPool("motd_set", e.b.vars(admin), got) {
		t.Fatalf("admin set: %q", got)
	}
	if got := e.b.currentMotd(); got != "Maintenance tonight at 9 [31mpm [0m" && strings.ContainsRune(got, 0x1b) {
		t.Fatalf("control characters must be stripped from the stored MOTD: %q", got)
	}
	say(user, "!motd")
	if got := e.motdPMs("User"); len(got) != 1 || !strings.Contains(got[0], "Maintenance tonight at 9") || len(e.h.said) != 0 {
		t.Fatalf("!motd should show the new text, to the asker only, in a box: motds=%v said=%v", e.h.motds, e.h.said)
	}
	e.logoff(user)
	e.h.reset()
	e.login(user)
	if got := e.motdPMs("User"); len(got) != 1 || !strings.Contains(got[0], "Maintenance tonight at 9") {
		t.Fatalf("the next login should get the new MOTD: %v", e.h.pms)
	}

	// Usage, length cap, and clearing.
	if got := say(admin, "!motd set"); !e.inPool("motd_usage", e.b.vars(admin), got) {
		t.Fatalf("empty set: %q", got)
	}
	say(admin, "!motd set "+strings.Repeat("x", 1000))
	if n := len(e.b.currentMotd()); n != maxMotdChars {
		t.Fatalf("MOTD should be capped at %d chars, got %d", maxMotdChars, n)
	}
	if got := say(admin, "!motd clear"); !e.inPool("motd_cleared", e.b.vars(admin), got) {
		t.Fatalf("clear: %q", got)
	}
	say(user, "!motd")
	if len(e.h.motds) != 0 || len(e.h.pms) != 1 || !e.inPool("motd_none", e.b.vars(user), e.h.pms[0].text) {
		t.Fatalf("!motd after clear should say there is none, privately: motds=%v pms=%v", e.h.motds, e.h.pms)
	}
	e.logoff(user)
	e.h.reset()
	e.login(user)
	if len(e.motdPMs("User")) != 0 {
		t.Fatalf("a cleared MOTD must not be sent at login: %v", e.h.pms)
	}
}

func TestMotdSurvivesARestart(t *testing.T) {
	e := newEnv(t)
	e.h.admins["SHA256:boss"] = true
	boss := keyed("boss", "Boss")
	e.login(boss)
	e.chat(boss, "x")
	e.advance(time.Minute)
	e.chat(boss, "!motd set Kept across restarts")
	_ = e.b.st.close()

	b2, err := New(e.h, Options{DBPath: e.dbPath, Now: func() time.Time { return e.clock }, Sleep: func(time.Duration) {}, Seed: [2]uint64{1, 2}})
	if err != nil {
		t.Fatal(err)
	}
	defer b2.st.close()
	if got := b2.currentMotd(); got != "Kept across restarts" {
		t.Fatalf("MOTD lost on restart: %q", got)
	}
	// ...including a deliberately cleared one (no fallback to the default).
	b2.st.set("motd", "")
	if got := b2.currentMotd(); got != "" {
		t.Fatalf("a cleared MOTD must stay cleared, got %q", got)
	}
}

// Mail is only deleted once it has really been delivered, so it can never be
// lost to the bot's output cap, a full queue, or a user who is ignoring him.
func TestTellIsKeptUntilItIsReallyDelivered(t *testing.T) {
	e := newEnv(t)
	alice, bob := keyed("a", "Alice"), keyed("b", "Bob")
	e.login(bob)
	e.logoff(bob)
	e.login(alice)
	e.chat(alice, "hi")
	e.advance(time.Minute)
	e.chat(alice, "!tell Bob mind the gap")
	if n := e.b.st.countTellsTo("SHA256:b"); n != 1 {
		t.Fatalf("setup: %d tells queued", n)
	}
	delivered := func() bool {
		for _, m := range e.h.pmsExceptMotd() {
			if strings.Contains(m.text, "mind the gap") {
				return true
			}
		}
		return false
	}

	// 1) Bob is ignoring Gus when it arrives: not delivered, not deleted.
	e.h.ignoring["Bob"] = true
	e.advance(time.Hour)
	e.h.reset()
	e.login(bob)
	if delivered() || e.b.st.countTellsTo("SHA256:b") != 1 {
		t.Fatalf("mail to a user ignoring Gus must stay queued: pms=%v left=%d", e.h.pms, e.b.st.countTellsTo("SHA256:b"))
	}
	if len(e.b.inflight) != 0 {
		t.Fatalf("an undelivered tell must not stay marked in flight: %v", e.b.inflight)
	}

	// 2) The bot's output cap drops the batch: the mail survives that too.
	e.logoff(bob)
	e.h.ignoring["Bob"] = false
	e.b.cfg.RateGlobalPerMin = 0
	e.advance(time.Hour)
	e.h.reset()
	e.login(bob)
	if delivered() || e.b.st.countTellsTo("SHA256:b") != 1 || len(e.b.inflight) != 0 {
		t.Fatalf("mail dropped by the output cap must stay queued: left=%d inflight=%v", e.b.st.countTellsTo("SHA256:b"), e.b.inflight)
	}

	// 3) A tell already on its way is not sent twice by a quick second login.
	e.logoff(bob)
	e.b.cfg.RateGlobalPerMin = 30
	var id int64
	for _, tr := range e.b.st.peekTells("SHA256:b") {
		id = tr.ID
	}
	e.b.inflight[id] = true
	e.advance(time.Hour)
	e.h.reset()
	e.login(bob)
	if delivered() {
		t.Fatal("a tell that is already in flight was sent again")
	}
	delete(e.b.inflight, id)

	// 4) Finally it is delivered, once, and only then deleted.
	e.logoff(bob)
	e.advance(time.Hour)
	e.h.reset()
	e.login(bob)
	if !delivered() || e.b.st.countTellsTo("SHA256:b") != 0 || len(e.b.inflight) != 0 {
		t.Fatalf("mail should now arrive and be deleted: pms=%v left=%d", e.h.pms, e.b.st.countTellsTo("SHA256:b"))
	}
	e.logoff(bob)
	e.h.reset()
	e.advance(time.Hour)
	e.login(bob)
	if delivered() {
		t.Fatal("delivered mail came back")
	}
}
