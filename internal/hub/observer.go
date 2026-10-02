package hub

// Observer lets an in-process integration (the SysOp bot) watch room
// activity. Callbacks run on a room's goroutine, so they must return quickly
// and must not call back into the Hub or Room synchronously; hand the event
// to another goroutine instead.
type Observer interface {
	// OnJoin fires after sess is in the room. login is true only for the
	// session's very first join (i.e. it just connected), not room hopping.
	OnJoin(room string, sess *Session, login bool)
	// OnPart fires after sess has left the room; disconnect is true when the
	// whole SSH connection ended.
	OnPart(room string, sess *Session, disconnect bool)
	// OnChat fires for a chat message or /me emote (action).
	OnChat(room string, sess *Session, body string, action bool)
}

// SetObserver installs the observer. Call before serving connections.
func (h *Hub) SetObserver(o Observer) { h.observer = o }

// Sessions returns every connected session across all rooms.
func (h *Hub) Sessions() []*Session {
	seen := map[*Session]bool{}
	var out []*Session
	for _, r := range h.loadedRooms() {
		respond := make(chan []*Session, 1)
		r.Send(evSessions{respond: respond})
		for _, s := range <-respond {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// markJoined reports whether this is the session's first-ever room join.
func (s *Session) markJoined() (first bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	first = !s.everJoined
	s.everJoined = true
	return first
}
