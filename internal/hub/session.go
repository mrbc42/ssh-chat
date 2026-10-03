package hub

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Session represents one connected SSH user.
type Session struct {
	FP          string // identity: pubkey fingerprint, or "anon-xxxxxxxx"
	IP          string
	ConnectedAt time.Time

	Outbox chan Outbound

	mu           sync.Mutex
	nick         string
	currentRoom  *Room
	afk          bool
	lastActivity time.Time

	ignored map[string]bool // lowercased nicks whose chat/actions/PMs are hidden (session only)

	everJoined bool

	// closed is set when the SSH connection has ended. A join that is still
	// queued in some room's actor when that happens must NOT add the dead
	// session (that is how "ghost" members were created under load), and done
	// lets a janitor notice sessions whose connection died without a part.
	closed atomic.Bool
	done   <-chan struct{}
	id     uint64

	joinNotice string // set before Join, consumed by Room.handleJoin

	limiter    *tokenBucket // chat messages
	cmdLimiter *tokenBucket // every slash command
	trusted    bool         // in-process integrations (the bot) skip the flood limits

	cooldowns  map[string]time.Time // per-command lockouts, by command name
	lastBody   string               // normalised last chat body, for repeat detection
	lastBodyAt time.Time
	repeats    int
	strikes    []time.Time // recent flood-limit violations
}

func NewSession(fp, ip, nick string) *Session {
	return &Session{
		id:           nextSessionID.Add(1),
		FP:           fp,
		IP:           ip,
		ConnectedAt:  time.Now(),
		Outbox:       make(chan Outbound, 64),
		nick:         nick,
		lastActivity: time.Now(),
		limiter:      newTokenBucket(10, 5), // burst 10, refill 5/sec
		cmdLimiter:   newTokenBucket(8, 2),  // burst 8, refill 2/sec
	}
}

func (s *Session) Nick() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nick
}

func (s *Session) SetNick(n string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nick = n
}

func (s *Session) CurrentRoom() *Room {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentRoom
}

func (s *Session) setCurrentRoom(r *Room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentRoom = r
}

// Touch records activity (a submitted chat message or command), clearing
// any away status and resetting the idle clock. It reports whether the
// session was away immediately before this call, so the caller can decide
// whether to announce a return.
func (s *Session) Touch() (wasAfk bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	wasAfk = s.afk
	s.afk = false
	s.lastActivity = time.Now()
	return wasAfk
}

func (s *Session) IsAfk() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.afk
}

// SetAfk sets away status directly, without touching the idle clock
// (going away due to idleness or /afk shouldn't reset it; coming back
// does, via Touch, not this).
func (s *Session) SetAfk(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afk = v
}

// IdleFor returns how long it's been since the session last submitted a
// chat message or command.
func (s *Session) IdleFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastActivity)
}

// Allow reports whether the session is within its chat-flood budget.
func (s *Session) Allow() bool {
	return s.isTrusted() || s.limiter.allow()
}

// AllowCommand is the flood gate for every slash command.
func (s *Session) AllowCommand() bool {
	return s.isTrusted() || s.cmdLimiter.allow()
}

// SetTrusted exempts a session from the per-user flood limits. Only for
// in-process integrations that do their own output limiting (the bot).
func (s *Session) SetTrusted(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trusted = v
}

func (s *Session) isTrusted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trusted
}

// CooldownLeft returns how long the named command is still locked for this
// session (0 = free to use).
func (s *Session) CooldownLeft(name string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if left := time.Until(s.cooldowns[name]); left > 0 {
		return left
	}
	return 0
}

// StartCooldown locks the named command for d (call after it succeeds).
func (s *Session) StartCooldown(name string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cooldowns == nil {
		s.cooldowns = make(map[string]time.Time)
	}
	s.cooldowns[name] = time.Now().Add(d)
}

const (
	repeatWindow = 10 * time.Second
	strikeWindow = 30 * time.Second
	// maxStrikes is deliberately high: a user who pastes a long block of text
	// trips the rate limit on many lines but must not be thrown off; a script
	// hammering the server reaches it within a second or two.
	maxStrikes = 60
)

// IsRepeat reports whether body is the third identical message in a row
// within repeatWindow (two repeats are allowed: "lol", "lol").
func (s *Session) IsRepeat(body string) bool {
	norm := strings.ToLower(strings.TrimSpace(body))
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if norm == s.lastBody && now.Sub(s.lastBodyAt) < repeatWindow {
		s.repeats++
	} else {
		s.repeats = 0
	}
	s.lastBody, s.lastBodyAt = norm, now
	return s.repeats >= 2
}

// Strike records a flood-limit violation and reports whether the session has
// now committed enough of them recently to be disconnected.
func (s *Session) Strike() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.trusted {
		return false
	}
	now := time.Now()
	kept := s.strikes[:0]
	for _, t := range s.strikes {
		if now.Sub(t) < strikeWindow {
			kept = append(kept, t)
		}
	}
	s.strikes = append(kept, now)
	return len(s.strikes) >= maxStrikes
}

var nextSessionID atomic.Uint64

// ID is a process-unique number for this connection.
func (s *Session) ID() uint64 { return s.id }

// MarkClosed records that the connection is over (see closed).
func (s *Session) MarkClosed() { s.closed.Store(true) }

// SetDone supplies a channel that closes when the SSH connection ends.
func (s *Session) SetDone(c <-chan struct{}) { s.done = c }

// gone reports whether the connection has ended, by either signal.
func (s *Session) gone() bool {
	if s.closed.Load() {
		return true
	}
	if s.done != nil {
		select {
		case <-s.done:
			return true
		default:
		}
	}
	return false
}

// SetJoinNotice sets a private informational line the room delivers to this
// session right after its first room switch. It must be set before Join:
// joining is asynchronous and the room switch clears the UI's scrollback, so
// a line sent directly would be wiped.
func (s *Session) SetJoinNotice(text string) { s.joinNotice = text }

// Ignore starts hiding chat, actions and PMs from nick (case-insensitive).
func (s *Session) Ignore(nick string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ignored == nil {
		s.ignored = make(map[string]bool)
	}
	s.ignored[strings.ToLower(nick)] = true
}

// Unignore reports whether nick was being ignored.
func (s *Session) Unignore(nick string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := strings.ToLower(nick)
	was := s.ignored[k]
	delete(s.ignored, k)
	return was
}

func (s *Session) IgnoredNicks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.ignored))
	for n := range s.ignored {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (s *Session) isIgnoring(nick string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ignored[strings.ToLower(nick)]
}

// ignoresLine reports whether l is a user-authored line from someone this
// session is ignoring. System lines and the like are never hidden.
func (s *Session) ignoresLine(l Line) bool {
	switch l.Kind {
	case KindChat, KindAction, KindPM:
		return l.Sender != "" && s.isIgnoring(l.Sender)
	}
	return false
}

// send delivers an Outbound to this session's UI without blocking the
// sender. If the session's outbox is full it is dropped after repeated
// failures the caller should disconnect the session (lagging client).
func (s *Session) send(o Outbound) bool {
	select {
	case s.Outbox <- o:
		return true
	default:
		return false
	}
}

// tokenBucket is a minimal, mutex-protected token bucket for per-session
// flood control (messages per second).
type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	max      float64
	refill   float64 // tokens per second
	lastTime time.Time
}

func newTokenBucket(burst int, perSecond float64) *tokenBucket {
	return &tokenBucket{
		tokens:   float64(burst),
		max:      float64(burst),
		refill:   perSecond,
		lastTime: time.Now(),
	}
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastTime).Seconds()
	b.lastTime = now
	b.tokens += elapsed * b.refill
	if b.tokens > b.max {
		b.tokens = b.max
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
