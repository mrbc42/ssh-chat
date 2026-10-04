package bot

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"
)

// Person is a connected human, as the bot sees them.
type Person struct {
	ID   string // unique per connection
	Nick string
	FP   string // SSH key fingerprint, or "anon-…" for keyless
	Anon bool
}

// Host is everything the bot needs from the chat server.
type Host interface {
	Say(text string)         // one line of public chat in #main
	SayIn(room, text string) // one line of public chat in another channel
	Action(text string)      // an emote in #main
	PM(nick, text string)    // a private message
	Online() []Person        // every connected human, server-wide (bot excluded)
	OnlineCount() int        // len(Online()) without building the list; may be up to a second stale
}

type EventKind int

const (
	EvLogin EventKind = iota
	EvLogoff
	EvChat
)

// Event is a thing that happened in the room, delivered to the bot.
type Event struct {
	Kind EventKind
	P    Person
	Body string // EvChat only
	Room string // EvChat only: the channel it was said in ("" or "main" = #main)
}

type outLine struct {
	room string // public lines: the channel to speak in ("" = #main)
	pmTo string // "" = public
	text string
	kind int
}

// Bot is the persona engine. All state is touched only from the Run loop
// (or directly from tests), so it needs no locking.
type Bot struct {
	cfg  Config
	c    *Content
	st   *botStore
	host Host

	now   func() time.Time
	sleep func(time.Duration)
	rng   *rand.Rand
	pk    *picker

	out           chan []outLine // nil = deliver synchronously (tests)
	userWin       map[string][]time.Time
	userWarned    map[string]time.Time
	globalWin     []time.Time
	unmatchedPath string

	startedAt    time.Time
	lastActivity time.Time
	humanSince   bool // a human did something since the bot last spoke unprompted
	lastLonely   time.Time
	trivia       map[string]*activeTrivia // one live question per channel ("main", "lounge", ...)
	curRoom      string                   // channel of the chat event being handled ("" = #main)
	nodeRecord   int
}

// Options are the runtime paths and test seams.
type Options struct {
	DBPath        string
	UnmatchedPath string
	DataDir       string // optional override directory for data files
	Now           func() time.Time
	Sleep         func(time.Duration)
	Seed          [2]uint64
}

// isMain reports whether room names #main ("" counts: that is how the bot
// itself and its scheduled posts refer to it).
func isMain(room string) bool { return room == "" || strings.EqualFold(room, "main") }

// New builds a bot. Call Run to start it.
func New(host Host, o Options) (*Bot, error) {
	cfg, content, err := Load(o.DataDir)
	if err != nil {
		return nil, err
	}
	if err := validatePools(content); err != nil {
		return nil, err
	}
	st, err := openStore(o.DBPath)
	if err != nil {
		return nil, fmt.Errorf("bot db: %w", err)
	}
	b := &Bot{
		cfg: cfg, c: content, st: st, host: host,
		now: o.Now, sleep: o.Sleep,
		trivia:  map[string]*activeTrivia{},
		userWin: map[string][]time.Time{}, userWarned: map[string]time.Time{},
		unmatchedPath: o.UnmatchedPath,
	}
	if b.now == nil {
		b.now = time.Now
	}
	if b.sleep == nil {
		b.sleep = time.Sleep
	}
	seed := o.Seed
	if seed == [2]uint64{} {
		seed = [2]uint64{uint64(time.Now().UnixNano()), 0x5359534f50}
	}
	b.rng = rand.New(rand.NewPCG(seed[0], seed[1]))
	b.pk = newPicker(b.rng, cfg.RecentMemory)
	b.startedAt = b.now()
	b.lastActivity = b.startedAt
	b.humanSince = true
	b.nodeRecord = st.getInt("node_record")
	st.purgeTells(b.now().Add(-time.Duration(cfg.TellExpireDays) * 24 * time.Hour))
	return b, nil
}

// Nick is the name the bot chats under.
func (b *Bot) Nick() string { return b.cfg.Nick }

// Run processes events until ctx ends. Output is paced by a separate
// goroutine so typing delays never block event handling.
func (b *Bot) Run(ctx context.Context, events <-chan Event) {
	b.out = make(chan []outLine, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case batch := <-b.out:
				b.deliver(batch)
			case <-ctx.Done():
				return
			}
		}
	}()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case ev := <-events:
			b.Handle(ev)
		case <-tick.C:
			b.Tick()
		case <-ctx.Done():
			<-done
			_ = b.st.close()
			return
		}
	}
}

// Handle processes one event.
func (b *Bot) Handle(ev Event) {
	switch ev.Kind {
	case EvLogin:
		b.onLogin(ev.P)
	case EvLogoff:
		b.onLogoff(ev.P)
	case EvChat:
		// Replies to a chat line go back to the channel it came from.
		if isMain(ev.Room) {
			b.curRoom = ""
		} else {
			b.curRoom = ev.Room
		}
		b.onChat(ev.P, ev.Body)
		b.curRoom = ""
	}
}

// vars returns the standard template variables for a person.
func (b *Bot) vars(p Person, extra ...string) map[string]string {
	v := map[string]string{"nick": clean(p.Nick, 24), "bot": b.cfg.Nick, "board": b.cfg.Board}
	for i := 0; i+1 < len(extra); i += 2 {
		v[extra[i]] = extra[i+1]
	}
	return v
}

func (b *Bot) text(pool string, vars map[string]string) string {
	return b.pk.poolText(pool, b.c.Pools[pool], vars)
}

// say queues public lines.
func (b *Bot) say(kind int, texts ...string) {
	var batch []outLine
	for _, t := range texts {
		if t != "" {
			batch = append(batch, outLine{room: b.curRoom, text: t, kind: kind})
		}
	}
	b.emit(batch)
}

func (b *Bot) pm(nick, text string) { b.emit([]outLine{{pmTo: nick, text: text}}) }

// emit applies the global output cap, then hands the batch to the speaker.
func (b *Bot) emit(batch []outLine) {
	if len(batch) == 0 {
		return
	}
	now := b.now()
	cut := now.Add(-time.Minute)
	kept := b.globalWin[:0]
	for _, t := range b.globalWin {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	b.globalWin = kept
	lines := 0 // count real chat messages, after any splitting of over-long text
	for _, l := range batch {
		lines += len(chunk(l.text, b.cfg.MaxMessageChars))
	}
	if len(b.globalWin)+lines > b.cfg.RateGlobalPerMin {
		return // over the global cap: stay silent rather than flood the room
	}
	for i := 0; i < lines; i++ {
		b.globalWin = append(b.globalWin, now)
	}
	if b.out == nil {
		b.deliver(batch)
		return
	}
	select {
	case b.out <- batch:
	default: // speaker backed up; drop rather than queue unboundedly
	}
}

// deliver paces and sends a batch, one chat message per item (the chat
// screen does the visual wrapping). It runs on the speaker goroutine
// (or inline in tests), so it must not touch other Bot state.
func (b *Bot) deliver(batch []outLine) {
	lo, hi := b.cfg.TypingDelayMs[0], b.cfg.TypingDelayMs[1]
	if hi > 0 {
		if b.cfg.TypingIndicator && batch[0].room == "" { // the indicator is a #main emote
			b.host.Action("is typing...")
		}
		d := lo
		if hi > lo {
			d += rand.IntN(hi - lo + 1)
		}
		b.sleep(time.Duration(d) * time.Millisecond)
	}
	for _, l := range batch {
		for _, line := range chunk(l.text, b.cfg.MaxMessageChars) {
			if strings.HasPrefix(line, "/") {
				line = "." + line // never let echoed text become a server command
			}
			line = colourise(b.cfg.Colour, l.kind, line)
			switch {
			case l.pmTo != "":
				b.host.PM(l.pmTo, line)
			case l.room != "":
				b.host.SayIn(l.room, line)
			default:
				b.host.Say(line)
			}
			if b.cfg.Baud > 0 {
				// ~10 bits per character on an async serial line.
				b.sleep(time.Duration(float64(len(line)*10) / float64(b.cfg.Baud) * float64(time.Second)))
			}
		}
	}
}

// allow enforces the per-user addressed-message rate limit.
func (b *Bot) allow(p Person) bool {
	key := p.FP
	if p.Anon {
		key = p.ID
	}
	now := b.now()
	cut := now.Add(-time.Minute)
	w := b.userWin[key][:0]
	for _, t := range b.userWin[key] {
		if t.After(cut) {
			w = append(w, t)
		}
	}
	if len(w) >= b.cfg.RatePerUserPerMin {
		b.userWin[key] = w
		if now.Sub(b.userWarned[key]) > 30*time.Second {
			b.userWarned[key] = now
			b.say(kindPlain, b.text("rate_limited", b.vars(p)))
		}
		return false
	}
	b.userWin[key] = append(w, now)
	return true
}

func (b *Bot) logUnmatched(p Person, text string) {
	if b.unmatchedPath == "" {
		return
	}
	f, err := os.OpenFile(b.unmatchedPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%s\n", b.now().Format(time.RFC3339), clean(p.Nick, 24), clean(text, 300))
}

// requiredPools lists every pool name the code uses; a data set missing any
// of them is rejected at startup rather than failing mid-conversation.
var requiredPools = []string{
	"login", "logoff", "greet_first", "greet_returning", "greet_returning_today", "greet_rapid", "greet_anon",
	"absence_30", "absence_90", "absence_365", "milestone_caller", "milestone_visit", "anniversary",
	"remark_late", "remark_early", "remark_weekend", "lonely", "idle_intro",
	"tell_stored", "tell_online", "tell_unknown", "tell_self", "tell_full", "tell_limit", "tell_usage", "tell_need_key", "tell_deliver",
	"first_message", "node_record", "quote_intro", "maintenance", "fallback", "rate_limited",
	"help_intro", "roll_result", "roll_bad", "time_reply", "who_reply", "who_empty",
	"seen_online", "seen_ago", "seen_unknown", "seen_usage", "stats_reply", "motd_intro", "rules_intro",
	"trivia_ask", "trivia_correct", "trivia_wrong", "trivia_timeout", "trivia_none", "trivia_already",
	"trivia_top", "trivia_top_empty", "trivia_anon",
	"forget_done", "forget_anon", "remember_done",
}

func validatePools(c *Content) error {
	for _, name := range requiredPools {
		if len(c.Pools[name]) == 0 {
			return fmt.Errorf("persona.json: missing or empty pool %q", name)
		}
	}
	if len(c.Trivia) == 0 || len(c.Fortunes) == 0 || len(c.Oneliners) == 0 || len(c.History) == 0 || len(c.Quotes) == 0 {
		return fmt.Errorf("trivia, fortunes, oneliners, history and quotes must not be empty")
	}
	return nil
}
