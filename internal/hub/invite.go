package hub

import (
	"fmt"
	"strings"
)

// resolveInvitee finds the key fingerprint and display name for a user named
// in /invite or /uninvite: someone online (any channel) or a known nickname.
func resolveInvitee(c *CmdCtx, nick string) (fp, name, problem string) {
	if s := c.Hub.FindSessionByNick(nick); s != nil {
		return s.FP, s.Nick(), ""
	}
	if f, ok, err := c.Store.FindFingerprintByNickname(c.ctx, nick); err == nil && ok {
		return f, nick, ""
	}
	return "", nick, fmt.Sprintf("No such user %q (they must be online, or known to the server by that name).", nick)
}

// cmdInvite lets someone into a locked channel. The invite is tied to their SSH
// key, so it follows them across connections; users without a key cannot be
// invited because their identity changes every time they connect.
func cmdInvite(c *CmdCtx, args []string) []string {
	room := c.Sess.CurrentRoom() // NeedsOp guarantees a channel other than #main
	pos := posArgs(args)
	if len(pos) == 0 {
		return listInvites(c, room)
	}
	fp, name, problem := resolveInvitee(c, pos[0])
	if problem != "" {
		return []string{problem}
	}
	if strings.HasPrefix(fp, "anon-") {
		return []string{fmt.Sprintf("%s has no SSH key, so an invite can't be kept for them: their identity changes every time they connect. They need a key first (type !ssh for the guide).", name)}
	}
	if err := c.Store.AddInvite(c.ctx, room.ID, fp, c.Sess.FP); err != nil {
		return []string{"Failed to save the invite."}
	}
	c.Hub.ShowLines(name, []string{fmt.Sprintf("%s invited you to #%s. Type /join %s to enter.", c.Sess.Nick(), room.Name(), room.Name())})
	note := ""
	if !room.Info().Locked {
		note = " (the channel is not locked right now; the invite applies whenever it is)"
	}
	return []string{fmt.Sprintf("Invited %s to #%s%s.", name, room.Name(), note)}
}

func cmdUninvite(c *CmdCtx, args []string) []string {
	room := c.Sess.CurrentRoom()
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /uninvite <user>"}
	}
	fp, name, problem := resolveInvitee(c, pos[0])
	if problem != "" {
		return []string{problem}
	}
	removed, err := c.Store.RemoveInvite(c.ctx, room.ID, fp)
	if err != nil {
		return []string{"Failed to withdraw the invite."}
	}
	if !removed {
		return []string{fmt.Sprintf("%s was not invited to #%s.", name, room.Name())}
	}
	return []string{fmt.Sprintf("Withdrew %s's invite to #%s. If they are inside they stay until they leave; they cannot rejoin while it is locked.", name, room.Name())}
}

func listInvites(c *CmdCtx, room *Room) []string {
	fps, err := c.Store.ListInvites(c.ctx, room.ID)
	if err != nil {
		return []string{"Internal error reading the invite list."}
	}
	state := "unlocked"
	if room.Info().Locked {
		state = "locked"
	}
	if len(fps) == 0 {
		return []string{fmt.Sprintf("#%s is %s. Nobody is invited. /invite <user> lets someone in while it is locked.", room.Name(), state)}
	}
	lines := []string{fmt.Sprintf("Invited to #%s (%s): the staff plus", room.Name(), state)}
	for _, fp := range fps {
		lines = append(lines, "  "+c.Hub.displayName(c, fp))
	}
	return lines
}

// displayName is a human label for a key fingerprint: the nickname of someone
// online, else the stored one, else the fingerprint.
func (h *Hub) displayName(c *CmdCtx, fp string) string {
	for _, s := range h.Sessions() {
		if s.FP == fp {
			return s.Nick() + " (online)"
		}
	}
	if nick, ok, _ := c.Store.NicknameFor(c.ctx, fp); ok {
		return nick + " (offline)"
	}
	return fp
}
