package hub

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mrbc42/ssh-chat/internal/store"
)

// cmdOwner lists who runs a channel: its owner and operators (online or not),
// or, for #main, the server administrators. It also says what the caller's own
// role is, which is what decides whether they see /lock, /topic and so on.
func cmdOwner(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	var ch store.Channel
	if len(pos) > 0 {
		var ok bool
		var err error
		name := strings.TrimPrefix(pos[0], "#")
		if ch, ok, err = c.Store.GetChannelByName(c.ctx, name); err != nil {
			return []string{"Internal error looking up that channel."}
		} else if !ok {
			return []string{fmt.Sprintf("No such channel #%s.", name)}
		}
	} else {
		room := c.Sess.CurrentRoom()
		if room == nil {
			room = c.Hub.Main()
		}
		var ok bool
		var err error
		if ch, ok, err = c.Store.GetChannelByID(c.ctx, room.ID); err != nil || !ok {
			return []string{"Internal error looking up this channel."}
		}
	}

	online := map[string]string{} // fingerprint -> nickname, for everyone connected now
	var adminsOnline []string
	for _, s := range c.Hub.Sessions() {
		online[s.FP] = s.Nick()
		if c.Hub.IsAdmin(s.FP) {
			adminsOnline = append(adminsOnline, s.Nick())
		}
	}
	sort.Strings(adminsOnline)
	who := func(fp string) string {
		switch {
		case fp == systemFP:
			return "nobody (a permanent channel has no human owner)"
		case online[fp] != "":
			return online[fp] + " (online)"
		case strings.HasPrefix(fp, "anon-"):
			return "a user who had no SSH key (gone: their identity ended when they disconnected)"
		}
		if nick, ok, _ := c.Store.NicknameFor(c.ctx, fp); ok {
			return nick + " (offline)"
		}
		return "unknown (" + fp + ")"
	}

	lines := []string{fmt.Sprintf("Staff of #%s:", ch.Name)}
	if ch.IsMain {
		lines = append(lines, "  #main has no channel owner or operators. It is run by the server administrators.")
		if len(adminsOnline) > 0 {
			lines = append(lines, "  Administrators online now: "+strings.Join(adminsOnline, ", "))
		} else {
			lines = append(lines, "  No administrator is online right now (use /admin <message> to alert one).")
		}
		return lines
	}

	ops, err := c.Store.ListOperators(c.ctx, ch.ID)
	if err != nil {
		return []string{"Internal error reading the staff list."}
	}
	var owners, operators []string
	for _, o := range ops {
		if o.Role == store.RoleOwner {
			owners = append(owners, who(o.FP))
		} else {
			operators = append(operators, who(o.FP))
		}
	}
	if len(owners) == 0 {
		owners = []string{"nobody"}
	}
	lines = append(lines, "  Owner:      "+strings.Join(owners, ", "))
	if len(operators) == 0 {
		lines = append(lines, "  Operators:  none")
	} else {
		lines = append(lines, "  Operators:  "+strings.Join(operators, ", "))
	}
	if c.Hub.IsPermanent(ch.Name) {
		lines = append(lines, "  This is a permanent channel: server administrators can moderate it too.")
		if len(adminsOnline) > 0 {
			lines = append(lines, "  Administrators online now: "+strings.Join(adminsOnline, ", "))
		}
	}

	// And what that means for the caller.
	role, _, _ := c.Store.IsOwnerOrOp(c.ctx, ch.ID, c.Sess.FP)
	if ch.Locked {
		lines = append(lines, "  This channel is LOCKED: only its staff and invited users can join.")
		if role != "" || c.Hub.IsAdmin(c.Sess.FP) { // the invite list is for staff eyes
			if fps, _ := c.Store.ListInvites(c.ctx, ch.ID); len(fps) > 0 {
				names := make([]string, len(fps))
				for i, fp := range fps {
					names[i] = who(fp)
				}
				lines = append(lines, "  Invited: "+strings.Join(names, ", "))
			}
		}
	}
	switch {
	case role == store.RoleOwner:
		lines = append(lines, "  You are the owner of this channel. Join it to use /lock, /topic, /op, /kick and /ban.")
	case role == store.RoleOperator:
		lines = append(lines, "  You are an operator of this channel. Join it to use /lock, /topic, /kick and /ban.")
	case c.Hub.IsAdmin(c.Sess.FP) && c.Hub.IsPermanent(ch.Name):
		lines = append(lines, "  You are a server administrator, so you have owner rights in this permanent channel.")
	case c.Hub.IsAdmin(c.Sess.FP):
		lines = append(lines, "  You are a server administrator: /takeover makes you its owner.")
	default:
		lines = append(lines, "  You are not staff in this channel.")
		if strings.HasPrefix(c.Sess.FP, "anon-") {
			lines = append(lines, "  You are connected without an SSH key, so you can't keep channel ownership between connections: connect with the same key every time (run ssh-add first if it has a passphrase).")
		}
	}
	return lines
}
