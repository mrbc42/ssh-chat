package hub

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrbc42/ssh-chat/internal/store"
)

// ErrRenamePermanent is returned when an admin tries to rename a channel that
// is listed as permanent in the server configuration.
var ErrRenamePermanent = errors.New("this channel is permanent (listed in the server's permanent channels setting); remove it there before renaming it")

// RenameRoom renames a channel (by an admin). The change is stored, the live
// room (if any) is re-registered under the new name, and its members are told
// and have their status bar refreshed. Not allowed for #main or a permanent
// channel (the configuration names it).
func (h *Hub) RenameRoom(ctx context.Context, oldName, newName string, by *Session) error {
	if err := validateChannelName(newName); err != nil {
		return err
	}
	ch, ok, err := h.store.GetChannelByName(ctx, oldName)
	if err != nil {
		return err
	}
	if !ok {
		return store.ErrChannelNotFound
	}
	if ch.IsMain {
		return errors.New("the main lobby cannot be renamed")
	}
	if h.IsPermanent(ch.Name) {
		return ErrRenamePermanent
	}
	if err := h.store.RenameChannel(ctx, ch.ID, newName); err != nil {
		return err
	}
	oldKey, newKey := strings.ToLower(ch.Name), strings.ToLower(newName)
	h.mu.Lock()
	r := h.rooms[oldKey]
	if r != nil {
		delete(h.rooms, oldKey)
		h.rooms[newKey] = r
	}
	h.mu.Unlock()
	if r != nil {
		r.Send(evRename{newName: newName, by: by})
	}
	return nil
}

// AssignOwner makes the user with key fingerprint fp the owner of the channel
// the admin is in, demoting any previous owner to operator, and announces it.
func (h *Hub) AssignOwner(ctx context.Context, room *Room, fp, nick string, by *Session) error {
	if err := h.store.SetOwner(ctx, room.ID, fp); err != nil {
		return err
	}
	if fp == by.FP {
		room.Send(evSystem{text: fmt.Sprintf("*** admin %s took over #%s as its owner ***", by.Nick(), room.Name()), persist: true})
	} else {
		room.Send(evSystem{text: fmt.Sprintf("*** %s was made the owner of #%s by admin %s ***", nick, room.Name(), by.Nick()), persist: true})
	}
	return nil
}
