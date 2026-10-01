package hub

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mrbc42/ssh-chat/internal/store"
)

type Hub struct {
	store     *store.Store
	startedAt time.Time
	adminFPs  map[string]bool

	mu    sync.RWMutex
	rooms map[string]*Room // keyed by lowercased channel name

	main *Room
}

func NewHub(st *store.Store, adminFPs map[string]bool) (*Hub, error) {
	h := &Hub{
		store:     st,
		startedAt: time.Now(),
		adminFPs:  adminFPs,
		rooms:     make(map[string]*Room),
	}
	ch, err := st.GetOrCreateMainChannel(context.Background())
	if err != nil {
		return nil, err
	}
	h.main = newRoom(h, st, ch)
	h.rooms[strings.ToLower(ch.Name)] = h.main
	return h, nil
}

func (h *Hub) Main() *Room { return h.main }

func (h *Hub) StartedAt() time.Time { return h.startedAt }

func (h *Hub) IsAdmin(fp string) bool { return h.adminFPs[fp] }

// GetLoadedRoom returns a resident room by name, if one is currently loaded.
func (h *Hub) GetLoadedRoom(name string) (*Room, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.rooms[strings.ToLower(name)]
	return r, ok
}

// GetOrLoadRoom returns the room, loading/creating its in-memory actor if the
// channel exists in the store but has no resident goroutine yet.
func (h *Hub) GetOrLoadRoom(ctx context.Context, ch store.Channel) *Room {
	key := strings.ToLower(ch.Name)
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[key]; ok {
		return r
	}
	r := newRoom(h, h.store, ch)
	h.rooms[key] = r
	return r
}

// ChannelSummary merges a persisted channel with its live member count (0 if
// the room has no resident actor, i.e. nobody is currently in it).
type ChannelSummary struct {
	Name     string
	Topic    string
	Locked   bool
	Announce bool
	Members  int
}

func (h *Hub) ListChannels(ctx context.Context) ([]ChannelSummary, error) {
	chans, err := h.store.ListChannels(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ChannelSummary, 0, len(chans))
	for _, c := range chans {
		members := 0
		if r, ok := h.GetLoadedRoom(c.Name); ok {
			members = r.Info().Members
		}
		out = append(out, ChannelSummary{Name: c.Name, Topic: c.Topic, Locked: c.Locked, Announce: c.Announce, Members: members})
	}
	return out, nil
}

// loadedRooms returns every room with a resident actor (i.e. has had at
// least one member since the server started) as a snapshot slice, safe to
// iterate without holding the lock.
func (h *Hub) loadedRooms() []*Room {
	h.mu.RLock()
	defer h.mu.RUnlock()
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	return rooms
}

func (h *Hub) TotalUsersOnline() int {
	total := 0
	for _, r := range h.loadedRooms() {
		total += r.Info().Members
	}
	return total
}

// FindSessionByNick looks up a currently-connected session by nickname
// (case-insensitive), searching every loaded room — used for /msg, which
// must work regardless of which room the sender and recipient are each in.
func (h *Hub) FindSessionByNick(nick string) *Session {
	for _, r := range h.loadedRooms() {
		respond := make(chan []*Session, 1)
		r.Send(evFindMember{nick: nick, respond: respond})
		if found := <-respond; len(found) > 0 {
			return found[0]
		}
	}
	return nil
}

// NotifyAdmins delivers text to every currently-connected admin session
// (across all rooms), returning whether at least one admin received it live.
func (h *Hub) NotifyAdmins(text string) bool {
	if len(h.adminFPs) == 0 {
		return false
	}
	delivered := false
	for _, r := range h.loadedRooms() {
		respond := make(chan []*Session, 1)
		r.Send(evFindAdmins{adminFPs: h.adminFPs, respond: respond})
		for _, sess := range <-respond {
			line := adminLine(text)
			if sess.send(Outbound{Line: &line}) {
				delivered = true
			}
		}
	}
	return delivered
}

// BroadcastAll sends an ephemeral (non-persisted) system line to every
// currently resident room, e.g. for a server-shutdown notice.
func (h *Hub) BroadcastAll(text string) {
	for _, r := range h.loadedRooms() {
		r.Send(evSystem{text: text, persist: false})
	}
}

// AdminBroadcast sends a persisted, distinctly-styled announcement (e.g.
// "server going down for maintenance") to every currently resident channel.
// Authorization (IsAdmin) is checked by the caller.
func (h *Hub) AdminBroadcast(text string) {
	for _, r := range h.loadedRooms() {
		r.Send(evAdminAnnounce{text: text})
	}
}

// handleLaggingSession is called when a non-blocking send to a session's
// outbox fails (buffer full). We don't force-disconnect on the first drop
// (a single dropped line is tolerable); repeated drops are tracked by the UI
// side reconnect logic in practice, so for v1 we just drop the line.
func (h *Hub) handleLaggingSession(sess *Session) {
	_ = sess // placeholder hook for future lag-kick policy
}
