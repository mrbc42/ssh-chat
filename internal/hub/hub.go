package hub

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mrbc42/ssh-chat/internal/filter"
	"github.com/mrbc42/ssh-chat/internal/store"
)

// AfkThreshold is how long a session can go without submitting a chat
// message or command before it's automatically marked away.
const AfkThreshold = 5 * time.Minute

type Hub struct {
	store     *store.Store
	startedAt time.Time
	adminFPs  map[string]bool
	afkAfter  time.Duration
	filter    *filter.Filter
	observer  Observer

	mu    sync.RWMutex
	rooms map[string]*Room // keyed by lowercased channel name

	main *Room
}

func NewHub(st *store.Store, adminFPs map[string]bool) (*Hub, error) {
	h := &Hub{
		store:     st,
		startedAt: time.Now(),
		adminFPs:  adminFPs,
		afkAfter:  AfkThreshold,
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

// SetAfkThreshold overrides how long a session may idle before being marked
// away. Call before serving connections.
func (h *Hub) SetAfkThreshold(d time.Duration) {
	if d > 0 {
		h.afkAfter = d
	}
}

// SetFilter installs a word filter applied to chat, actions and PMs. Call
// before serving connections; nil disables filtering.
func (h *Hub) SetFilter(f *filter.Filter) { h.filter = f }

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

// CheckIdle marks sess away, announcing it in whatever room they're
// currently in, if they've gone AfkThreshold without submitting a chat
// message or command. Intended to be polled periodically (the UI's
// once-a-second tick) rather than driven by a dedicated timer per session.
func (h *Hub) CheckIdle(sess *Session) {
	if sess.IsAfk() || sess.IdleFor() < h.afkAfter {
		return
	}
	sess.SetAfk(true)
	room := sess.CurrentRoom()
	if room == nil {
		room = h.Main()
	}
	room.Send(evSystem{text: fmt.Sprintf("*** %s is away ***", sess.Nick()), persist: true})
}

// NickAvailable reports whether sess may use nick: no other online session
// has it, and no other keyed identity has persisted it.
func (h *Hub) NickAvailable(ctx context.Context, nick string, sess *Session) bool {
	if other := h.FindSessionByNick(nick); other != nil && other != sess {
		return false
	}
	taken, err := h.store.NicknameTaken(ctx, nick, sess.FP)
	return err == nil && !taken
}

// ResolveInitialNick picks the nickname for a new connection: the identity's
// saved one, else its deterministic default (with a numeric suffix if that is
// taken). Only keyed identities are persisted.
func (h *Hub) ResolveInitialNick(ctx context.Context, fp string) string {
	keyed := !strings.HasPrefix(fp, "anon-")
	if keyed {
		if nick, known, err := h.store.ResolveNickname(ctx, fp); err == nil && known {
			return nick
		}
	}
	base := DefaultNickFor(fp)
	nick := base
	probe := &Session{FP: fp}
	for i := 2; !h.NickAvailable(ctx, nick, probe) && i < 1000; i++ {
		nick = fmt.Sprintf("%s%d", base, i)
	}
	if keyed {
		_ = h.store.SetNickname(ctx, fp, nick)
	}
	return nick
}

// RecordDisconnect stamps a keyed identity's last-seen time.
func (h *Hub) RecordDisconnect(sess *Session) {
	if !strings.HasPrefix(sess.FP, "anon-") {
		_ = h.store.TouchIdentity(context.Background(), sess.FP)
	}
}

// DeleteRoom force-deletes a non-main channel: members are moved to #main and
// the channel, its messages, operators and bans are removed.
func (h *Hub) DeleteRoom(ctx context.Context, name string) error {
	ch, ok, err := h.store.GetChannelByName(ctx, name)
	if err != nil {
		return err
	}
	if !ok {
		return store.ErrChannelNotFound
	}
	if ch.IsMain {
		return errors.New("the main lobby cannot be deleted")
	}
	key := strings.ToLower(ch.Name)
	h.mu.Lock()
	r := h.rooms[key]
	delete(h.rooms, key)
	h.mu.Unlock()
	if r != nil {
		done := make(chan struct{})
		r.Send(evShutdown{done: done})
		<-done
	}
	return h.store.DeleteChannel(ctx, ch.ID)
}

// ExpireRooms deletes empty channels with no activity for olderThan and
// returns their names. Rooms with anyone in them are never expired.
func (h *Hub) ExpireRooms(ctx context.Context, olderThan time.Duration) ([]string, error) {
	stale, err := h.store.StaleChannels(ctx, time.Now().Add(-olderThan))
	if err != nil {
		return nil, err
	}
	var gone []string
	for _, ch := range stale {
		if r, ok := h.GetLoadedRoom(ch.Name); ok && r.Info().Members > 0 {
			continue
		}
		if err := h.DeleteRoom(ctx, ch.Name); err != nil {
			return gone, err
		}
		gone = append(gone, ch.Name)
	}
	return gone, nil
}

// mask applies the configured word filter.
func (h *Hub) mask(s string) string { return h.filter.Mask(s) }

// handleLaggingSession is called when a non-blocking send to a session's
// outbox fails (buffer full). We don't force-disconnect on the first drop
// (a single dropped line is tolerable); repeated drops are tracked by the UI
// side reconnect logic in practice, so for v1 we just drop the line.
func (h *Hub) handleLaggingSession(sess *Session) {
	_ = sess // placeholder hook for future lag-kick policy
}
