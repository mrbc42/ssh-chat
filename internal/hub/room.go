package hub

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mrbc42/ssh-chat/internal/store"
)

const scrollbackReplayLimit = 50

// Whole-channel message cap: a burst of 40, sustained 15 per second.
const (
	roomBurst     = 40
	roomPerSecond = 15
)

// Room is an actor: all of its mutable state (members, locked, topic,
// announce) is only ever touched from within its own run() goroutine, so no
// locking is needed for membership or broadcast ordering.
type Room struct {
	hub   *Hub
	store *store.Store

	ID   int64
	Name string

	events chan roomEvent

	// limiter caps the whole channel's message rate, on top of the per-user
	// limits, so many connections together cannot swamp it.
	limiter *tokenBucket

	members  map[*Session]struct{}
	locked   bool
	announce bool
	topic    string
}

func newRoom(h *Hub, st *store.Store, ch store.Channel) *Room {
	r := &Room{
		hub:      h,
		store:    st,
		ID:       ch.ID,
		Name:     ch.Name,
		events:   make(chan roomEvent, 256),
		limiter:  newTokenBucket(roomBurst, roomPerSecond),
		members:  make(map[*Session]struct{}),
		locked:   ch.Locked,
		announce: ch.Announce,
		topic:    ch.Topic,
	}
	go r.run()
	return r
}

// Send enqueues an event for the room's actor loop. It blocks if the actor
// is backed up (the queue is generously buffered; this is not on the
// session-delivery hot path, which uses non-blocking sends instead).
func (r *Room) Send(ev roomEvent) {
	r.events <- ev
}

func (r *Room) Info() RoomInfo {
	respond := make(chan RoomInfo, 1)
	r.Send(evSnapshot{respond: respond})
	return <-respond
}

// Join enqueues sess to join this room (see evJoin handling in run()).
func (r *Room) Join(sess *Session) {
	r.Send(evJoin{sess: sess})
}

// Part enqueues sess to leave this room (see evPart handling in run()).
func (r *Room) Part(sess *Session) {
	r.Send(evPart{sess: sess})
}

// PartSwitching enqueues sess to leave this room specifically because
// they're switching into another channel (used only for leaving #main),
// producing a "has joined another channel" announcement instead of the
// generic "has left" one.
func (r *Room) PartSwitching(sess *Session) {
	r.Send(evPart{sess: sess, reason: partReasonSwitch})
}

// PartDisconnect enqueues sess to leave this room because their whole SSH
// session ended, producing a "has logged off" announcement.
func (r *Room) PartDisconnect(sess *Session) {
	r.Send(evPart{sess: sess, reason: partReasonDisconnect})
}

func (r *Room) Who() []string {
	respond := make(chan []string, 1)
	r.Send(evWho{respond: respond})
	return <-respond
}

func (r *Room) run() {
	ctx := context.Background()
	for ev := range r.events {
		switch e := ev.(type) {
		case evJoin:
			r.handleJoin(ctx, e.sess)
		case evPart:
			r.handlePart(ctx, e.sess, e.reason)
		case evChat:
			r.handleChat(ctx, e.sess, e.body)
		case evShutdown:
			r.handleShutdown(e.done)
			// Late senders that still hold a reference must not block
			// forever on a dead actor; drain briefly, then let it go.
			deadline := time.After(time.Minute)
			for {
				select {
				case <-r.events:
				case <-deadline:
					return
				}
			}
		case evAction:
			r.handleAction(ctx, e.sess, e.body)
		case evSystem:
			r.broadcastSystem(ctx, e.text, e.persist)
		case evForceRemove:
			r.handleForceRemove(ctx, e.target, e.broadcastText, e.targetText)
		case evSetLocked:
			r.locked = e.locked
			_ = r.store.SetLocked(ctx, r.ID, e.locked)
			state := "unlocked"
			if e.locked {
				state = "locked"
			}
			r.broadcastSystem(ctx, fmt.Sprintf("*** #%s was %s by %s ***", r.Name, state, e.by.Nick()), true)
		case evSetAnnounce:
			r.announce = e.announce
			_ = r.store.SetAnnounce(ctx, r.ID, e.announce)
			state := "off"
			if e.announce {
				state = "on"
			}
			r.broadcastSystem(ctx, fmt.Sprintf("*** join/leave announcements in #%s turned %s by %s ***", r.Name, state, e.by.Nick()), true)
		case evSetTopic:
			r.topic = e.topic
			_ = r.store.SetTopic(ctx, r.ID, e.topic)
			r.broadcastSystem(ctx, fmt.Sprintf("*** %s set the topic: %s ***", e.by.Nick(), e.topic), true)
		case evWho:
			names := make([]string, 0, len(r.members))
			for sess := range r.members {
				names = append(names, sess.Nick())
			}
			e.respond <- names
		case evSnapshot:
			e.respond <- RoomInfo{
				Name:     r.Name,
				Topic:    r.topic,
				Locked:   r.locked,
				Announce: r.announce,
				Members:  len(r.members),
			}
		case evFindMember:
			var found []*Session
			for sess := range r.members {
				if strings.EqualFold(sess.Nick(), e.nick) {
					found = append(found, sess)
				}
			}
			e.respond <- found
		case evReap:
			for sess := range r.members {
				if sess.gone() {
					r.handlePart(ctx, sess, partReasonDisconnect)
				}
			}
		case evSessions:
			all := make([]*Session, 0, len(r.members))
			for sess := range r.members {
				all = append(all, sess)
			}
			e.respond <- all
		case evFindAdmins:
			var found []*Session
			for sess := range r.members {
				if e.adminFPs[sess.FP] {
					found = append(found, sess)
				}
			}
			e.respond <- found
		case evAdminAnnounce:
			r.persistAdmin(ctx, e.text)
			r.broadcastAll(adminLine(e.text))
		}
	}
}

func (r *Room) handleJoin(ctx context.Context, sess *Session) {
	// A join can sit in this room's queue for seconds under load. If the user
	// disconnected meanwhile (their part went to the room they were in then),
	// adding them now would create a ghost member nobody ever removes.
	if sess.gone() {
		return
	}
	// Fetch scrollback before persisting/broadcasting this join, so the
	// joiner's own "has joined" line doesn't show up duplicated in their
	// own history replay.
	msgs, err := r.store.RecentMessages(ctx, r.ID, scrollbackReplayLimit)
	var lines []Line
	if err == nil {
		for _, m := range msgs {
			if l := lineFromMessage(m); !sess.ignoresLine(l) {
				lines = append(lines, l)
			}
		}
	}

	r.members[sess] = struct{}{}
	sess.setCurrentRoom(r)

	sess.send(Outbound{
		SwitchRoom: &RoomInfo{Name: r.Name, Topic: r.topic, Locked: r.locked, Announce: r.announce, Members: len(r.members)},
		Scrollback: lines,
	})
	if sess.joinNotice != "" {
		line := infoLine(sess.joinNotice)
		sess.send(Outbound{Line: &line})
		sess.joinNotice = ""
	}

	if r.announce {
		text := fmt.Sprintf("*** %s has joined #%s ***", sess.Nick(), r.Name)
		r.persistSystem(ctx, text)
		r.broadcastExcept(sess, systemLine(text))
	}
	if o := r.hub.observer; o != nil {
		o.OnJoin(r.Name, sess, sess.markJoined())
	}
}

func (r *Room) handlePart(ctx context.Context, sess *Session, reason partReason) {
	if _, ok := r.members[sess]; !ok {
		return
	}
	delete(r.members, sess)
	if o := r.hub.observer; o != nil {
		defer o.OnPart(r.Name, sess, reason == partReasonDisconnect)
	}
	if !r.announce {
		return
	}
	var text string
	switch reason {
	case partReasonSwitch:
		text = fmt.Sprintf("*** %s has joined another channel ***", sess.Nick())
	case partReasonDisconnect:
		text = fmt.Sprintf("*** %s has logged off ***", sess.Nick())
	default:
		text = fmt.Sprintf("*** %s has left #%s ***", sess.Nick(), r.Name)
	}
	r.persistSystem(ctx, text)
	r.broadcastExcept(sess, systemLine(text))
}

// roomAllows applies the channel-wide cap; a refused message is dropped and
// only its sender is told.
func (r *Room) roomAllows(sess *Session) bool {
	if r.limiter.allow() {
		return true
	}
	line := errorLine(fmt.Sprintf("#%s is receiving too many messages right now; yours was dropped.", r.Name))
	sess.send(Outbound{Line: &line})
	return false
}

func (r *Room) handleChat(ctx context.Context, sess *Session, body string) {
	if !r.roomAllows(sess) {
		return
	}
	m := store.Message{ChannelID: r.ID, SenderFP: sess.FP, SenderName: sess.Nick(), Body: body, Kind: "msg"}
	_, _ = r.store.AppendMessage(ctx, m)
	r.broadcastAll(chatLine(sess.Nick(), body))
	if o := r.hub.observer; o != nil {
		o.OnChat(r.Name, sess, body, false)
	}
}

// handleShutdown evicts every member into #main with an explanation; the
// room is already unregistered from the hub by the caller.
func (r *Room) handleShutdown(done chan struct{}) {
	for sess := range r.members {
		if sess.gone() {
			delete(r.members, sess)
			continue
		}
		line := infoLine(fmt.Sprintf("#%s was deleted. Moving you to #main.", r.Name))
		sess.send(Outbound{Line: &line})
		delete(r.members, sess)
		sess.setCurrentRoom(nil)
		r.hub.Main().Join(sess)
	}
	close(done)
}

func (r *Room) handleAction(ctx context.Context, sess *Session, body string) {
	if !r.roomAllows(sess) {
		return
	}
	m := store.Message{ChannelID: r.ID, SenderFP: sess.FP, SenderName: sess.Nick(), Body: body, Kind: "action"}
	_, _ = r.store.AppendMessage(ctx, m)
	r.broadcastAll(actionLine(sess.Nick(), body))
	if o := r.hub.observer; o != nil {
		o.OnChat(r.Name, sess, body, true)
	}
}

func (r *Room) handleForceRemove(ctx context.Context, target *Session, broadcastText, targetText string) {
	if _, ok := r.members[target]; !ok {
		return
	}
	delete(r.members, target)
	r.persistSystem(ctx, broadcastText)
	r.broadcastAll(systemLine(broadcastText))
	target.send(Outbound{Line: &Line{Time: time.Now(), Kind: KindError, Body: targetText}})

	main := r.hub.Main()
	if main != r {
		main.Send(evJoin{sess: target})
	}
}

func (r *Room) broadcastSystem(ctx context.Context, text string, persist bool) {
	if persist {
		r.persistSystem(ctx, text)
	}
	r.broadcastAll(systemLine(text))
}

func (r *Room) persistSystem(ctx context.Context, text string) {
	_, _ = r.store.AppendMessage(ctx, store.Message{ChannelID: r.ID, SenderFP: "system", SenderName: "system", Body: text, Kind: "system"})
}

func (r *Room) persistAdmin(ctx context.Context, text string) {
	_, _ = r.store.AppendMessage(ctx, store.Message{ChannelID: r.ID, SenderFP: "admin", SenderName: "admin", Body: text, Kind: "admin"})
}

func (r *Room) broadcastAll(line Line) {
	for sess := range r.members {
		if sess.ignoresLine(line) {
			continue
		}
		l := line
		if !sess.send(Outbound{Line: &l}) {
			r.hub.handleLaggingSession(sess)
		}
	}
}

func (r *Room) broadcastExcept(except *Session, line Line) {
	for sess := range r.members {
		if sess == except || sess.ignoresLine(line) {
			continue
		}
		l := line
		if !sess.send(Outbound{Line: &l}) {
			r.hub.handleLaggingSession(sess)
		}
	}
}

func lineFromMessage(m store.Message) Line {
	switch m.Kind {
	case "system":
		return Line{Time: m.CreatedAt, Kind: KindSystem, Body: m.Body}
	case "admin":
		return Line{Time: m.CreatedAt, Kind: KindAdmin, Body: m.Body}
	case "action":
		return Line{Time: m.CreatedAt, Kind: KindAction, Sender: m.SenderName, Body: m.Body}
	default:
		return Line{Time: m.CreatedAt, Kind: KindChat, Sender: m.SenderName, Body: m.Body}
	}
}
