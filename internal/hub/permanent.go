package hub

import (
	"context"
	"fmt"
	"strings"

	"github.com/mrbc42/ssh-chat/internal/store"
)

// systemFP is the nominal creator of permanent channels created from config.
// It never connects, so such a channel has no human owner; server admins
// moderate it instead (see effectiveRole).
const systemFP = "system"

// ErrPermanent is returned when something tries to delete a permanent channel.
var ErrPermanent = fmt.Errorf("this channel is permanent: it is listed in the server's permanent channels setting, so remove it there to delete it")

// EnsurePermanent creates each named channel if it does not exist yet and marks
// it permanent: it is never expired and /delroom refuses it. Call once at
// startup, before serving. Names that already exist (created by a user) are
// kept as they are, owner included. #main is always permanent and may be listed.
func (h *Hub) EnsurePermanent(ctx context.Context, names []string) error {
	perm := map[string]bool{}
	for _, name := range names {
		if strings.EqualFold(name, store.MainChannelName) {
			continue
		}
		if err := validateChannelName(name); err != nil {
			return fmt.Errorf("permanent channel %q: %w", name, err)
		}
		if _, ok, err := h.store.GetChannelByName(ctx, name); err != nil {
			return err
		} else if !ok {
			if _, err := h.store.CreateChannel(ctx, name, systemFP); err != nil {
				return fmt.Errorf("creating permanent channel %q: %w", name, err)
			}
		}
		perm[strings.ToLower(name)] = true
	}
	h.permanent = perm
	return nil
}

// IsPermanent reports whether name is a configured permanent channel.
func (h *Hub) IsPermanent(name string) bool { return h.permanent[strings.ToLower(name)] }

// effectiveRole is sess's role in room for permission checks: the stored
// owner/operator role, except that a server admin has owner rights in
// permanent channels (which have no human owner to moderate them).
func (h *Hub) effectiveRole(ctx context.Context, sess *Session, room *Room) (string, error) {
	role, _, err := h.store.IsOwnerOrOp(ctx, room.ID, sess.FP)
	if err != nil {
		return "", err
	}
	if role == "" && h.IsAdmin(sess.FP) && h.IsPermanent(room.Name) {
		return store.RoleOwner, nil
	}
	return role, nil
}

// SayIn posts a chat line from sess into the named channel, which need not be
// the channel sess is in (the bot answers in whichever channel it was asked).
func (h *Hub) SayIn(sess *Session, roomName, body string) {
	r, ok := h.GetLoadedRoom(roomName)
	if !ok {
		return // nobody has been in the channel since startup, so nobody is listening
	}
	if len(body) > maxMessageLen {
		body = body[:maxMessageLen]
	}
	r.Send(evChat{sess: sess, body: h.mask(body)})
}
