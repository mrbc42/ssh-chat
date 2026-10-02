package hub

import (
	"sort"
	"strings"
	"sync"
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

	joinNotice string // set before Join, consumed by Room.handleJoin

	limiter *tokenBucket
}

func NewSession(fp, ip, nick string) *Session {
	return &Session{
		FP:           fp,
		IP:           ip,
		ConnectedAt:  time.Now(),
		Outbox:       make(chan Outbound, 64),
		nick:         nick,
		lastActivity: time.Now(),
		limiter:      newTokenBucket(10, 5), // burst 10, refill 5/sec
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
	return s.limiter.allow()
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
