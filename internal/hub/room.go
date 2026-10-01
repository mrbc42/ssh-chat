package hub

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mrbc42/ssh-chat/internal/store"
)

const scrollbackReplayLimit = 50

// Room is an actor: all of its mutable state (members, locked, topic,
// announce) is only ever touched from within its own run() goroutine, so no
// locking is needed for membership or broadcast ordering.
type Room struct {
	hub   *Hub
	store *store.Store

	ID   int64
	Name string

	events chan roomEvent

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
			r.handlePart(ctx, e.sess)
		case evChat:
			r.handleChat(ctx, e.sess, e.body)
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
		case evFindAdmins:
			var found []*Session
			for sess := range r.members {
				if e.adminFPs[sess.FP] {
					found = append(found, sess)
				}
			}
			e.respond <- found
		}
	}
}

func (r *Room) handleJoin(ctx context.Context, sess *Session) {
	// Fetch scrollback before persisting/broadcasting this join, so the
	// joiner's own "has joined" line doesn't show up duplicated in their
	// own history replay.
	msgs, err := r.store.RecentMessages(ctx, r.ID, scrollbackReplayLimit)
	var lines []string
	if err == nil {
		for _, m := range msgs {
			lines = append(lines, renderMessage(m))
		}
	}

	r.members[sess] = struct{}{}
	sess.setCurrentRoom(r)

	sess.send(Outbound{
		SwitchRoom: &RoomInfo{Name: r.Name, Topic: r.topic, Locked: r.locked, Announce: r.announce, Members: len(r.members)},
		Scrollback: lines,
	})

	if r.announce {
		text := fmt.Sprintf("*** %s has joined #%s ***", sess.Nick(), r.Name)
		r.persistSystem(ctx, text)
		r.broadcastExcept(sess, text)
	}
}

func (r *Room) handlePart(ctx context.Context, sess *Session) {
	if _, ok := r.members[sess]; !ok {
		return
	}
	delete(r.members, sess)
	if r.announce {
		text := fmt.Sprintf("*** %s has left #%s ***", sess.Nick(), r.Name)
		r.persistSystem(ctx, text)
		r.broadcastExcept(sess, text)
	}
}

func (r *Room) handleChat(ctx context.Context, sess *Session, body string) {
	m := store.Message{ChannelID: r.ID, SenderFP: sess.FP, SenderName: sess.Nick(), Body: body, Kind: "msg"}
	_, _ = r.store.AppendMessage(ctx, m)
	m.CreatedAt = time.Now()
	line := renderMessage(m)
	r.broadcastAll(line)
}

func (r *Room) handleForceRemove(ctx context.Context, target *Session, broadcastText, targetText string) {
	if _, ok := r.members[target]; !ok {
		return
	}
	delete(r.members, target)
	r.persistSystem(ctx, broadcastText)
	r.broadcastAll(broadcastText)
	target.send(Outbound{Line: targetText})

	main := r.hub.Main()
	if main != r {
		main.Send(evJoin{sess: target})
	}
}

func (r *Room) broadcastSystem(ctx context.Context, text string, persist bool) {
	if persist {
		r.persistSystem(ctx, text)
	}
	r.broadcastAll(text)
}

func (r *Room) persistSystem(ctx context.Context, text string) {
	_, _ = r.store.AppendMessage(ctx, store.Message{ChannelID: r.ID, SenderFP: "system", SenderName: "system", Body: text, Kind: "system"})
}

func (r *Room) broadcastAll(line string) {
	for sess := range r.members {
		if !sess.send(Outbound{Line: line}) {
			r.hub.handleLaggingSession(sess)
		}
	}
}

func (r *Room) broadcastExcept(except *Session, line string) {
	for sess := range r.members {
		if sess == except {
			continue
		}
		if !sess.send(Outbound{Line: line}) {
			r.hub.handleLaggingSession(sess)
		}
	}
}

func renderMessage(m store.Message) string {
	ts := m.CreatedAt.Format("15:04")
	if m.Kind == "system" {
		return fmt.Sprintf("[%s] %s", ts, m.Body)
	}
	return fmt.Sprintf("[%s] %s: %s", ts, m.SenderName, m.Body)
}
