package hub

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mrbc42/ssh-chat/internal/store"
)

const maxMessageLen = 500

var channelNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,24}$`)
var nickRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,24}$`)

// CmdCtx is the context passed to every slash-command handler.
type CmdCtx struct {
	Hub   *Hub
	Store *store.Store
	Sess  *Session
	ctx   context.Context
}

type Command struct {
	Name      string
	Aliases   []string
	Usage     string
	Help      string
	NeedsOp   bool // owner or operator of the current room required
	OwnerOnly bool // owner of the current room required
	Fn        func(c *CmdCtx, args []string) []string
}

var registry = map[string]*Command{}
var orderedNames []string

func register(c *Command) {
	registry[c.Name] = c
	orderedNames = append(orderedNames, c.Name)
	for _, a := range c.Aliases {
		registry[a] = c
	}
}

func init() {
	register(&Command{
		Name: "help", Usage: "/help", Help: "Show this help message.",
		Fn: cmdHelp,
	})
	register(&Command{
		Name: "nick", Usage: "/nick <name>", Help: "Change your display name.",
		Fn: cmdNick,
	})
	register(&Command{
		Name: "list", Aliases: []string{"rooms"}, Usage: "/list", Help: "List all channels (name, users, lock state).",
		Fn: cmdList,
	})
	register(&Command{
		Name: "create", Usage: "/create <name>", Help: "Create a new channel; you become its owner.",
		Fn: cmdCreate,
	})
	register(&Command{
		Name: "join", Usage: "/join <name>", Help: "Join a channel (creating it if it doesn't exist).",
		Fn: cmdJoin,
	})
	register(&Command{
		Name: "leave", Aliases: []string{"part"}, Usage: "/leave", Help: "Leave the current channel and return to #main.",
		Fn: cmdLeave,
	})
	register(&Command{
		Name: "who", Usage: "/who", Help: "List users in the current channel.",
		Fn: cmdWho,
	})
	register(&Command{
		Name: "topic", Usage: "/topic <text>", Help: "Set the current channel's topic.",
		NeedsOp: true, Fn: cmdTopic,
	})
	register(&Command{
		Name: "announce", Usage: "/announce on|off", Help: "Toggle join/leave announcements for this channel.",
		NeedsOp: true, Fn: cmdAnnounce,
	})
	register(&Command{
		Name: "kick", Usage: "/kick <user>", Help: "Kick a user from the current channel.",
		NeedsOp: true, Fn: cmdKick,
	})
	register(&Command{
		Name: "ban", Usage: "/ban <user> [reason]", Help: "Kick and ban a user from the current channel.",
		NeedsOp: true, Fn: cmdBan,
	})
	register(&Command{
		Name: "unban", Usage: "/unban <user>", Help: "Remove a channel ban.",
		NeedsOp: true, Fn: cmdUnban,
	})
	register(&Command{
		Name: "op", Usage: "/op <user>", Help: "Grant operator status in the current channel.",
		OwnerOnly: true, Fn: cmdOp,
	})
	register(&Command{
		Name: "deop", Usage: "/deop <user>", Help: "Revoke operator status in the current channel.",
		OwnerOnly: true, Fn: cmdDeop,
	})
	register(&Command{
		Name: "lock", Usage: "/lock", Help: "Lock the current channel (blocks new joins; still listed).",
		NeedsOp: true, Fn: cmdLock,
	})
	register(&Command{
		Name: "unlock", Usage: "/unlock", Help: "Unlock the current channel.",
		NeedsOp: true, Fn: cmdUnlock,
	})
	register(&Command{
		Name: "admin", Usage: "/admin <message>", Help: "Alert a connected administrator about abuse.",
		Fn: cmdAdmin,
	})
	register(&Command{
		Name: "quit", Aliases: []string{"exit"}, Usage: "/quit", Help: "Disconnect.",
		Fn: cmdQuit,
	})
	sort.Strings(orderedNames)
}

// HandleInput is the single entry point the UI calls for each submitted
// line: either a slash command or a plain chat message.
func HandleInput(ctx context.Context, h *Hub, sess *Session, line string) {
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return
	}
	if !strings.HasPrefix(line, "/") {
		sendChat(ctx, h, sess, line)
		return
	}
	Dispatch(ctx, h, sess, line)
}

func sendChat(ctx context.Context, h *Hub, sess *Session, body string) {
	if !sess.Allow() {
		line := errorLine("You're sending messages too fast. Slow down.")
		sess.send(Outbound{Line: &line})
		return
	}
	if len(body) > maxMessageLen {
		body = body[:maxMessageLen]
	}
	room := sess.CurrentRoom()
	if room == nil {
		room = h.Main()
	}
	room.Send(evChat{sess: sess, body: body})
}

func Dispatch(ctx context.Context, h *Hub, sess *Session, line string) {
	fields := strings.SplitN(strings.TrimPrefix(line, "/"), " ", 2)
	name := strings.ToLower(fields[0])
	var rest string
	if len(fields) > 1 {
		rest = strings.TrimSpace(fields[1])
	}
	var args []string
	if rest != "" {
		args = strings.Fields(rest)
	}

	cmd, ok := registry[name]
	if !ok {
		sendError(sess, fmt.Sprintf("Unknown command /%s. Type /help for a list of commands.", name))
		return
	}

	c := &CmdCtx{Hub: h, Store: h.store, Sess: sess, ctx: ctx}

	if cmd.NeedsOp || cmd.OwnerOnly {
		room := sess.CurrentRoom()
		if room == nil || room == h.Main() {
			sendError(sess, "That command can't be used in #main.")
			return
		}
		role, _, err := h.store.IsOwnerOrOp(ctx, room.ID, sess.FP)
		if err != nil {
			sendError(sess, "Internal error checking permissions.")
			return
		}
		if cmd.OwnerOnly && role != store.RoleOwner {
			sendError(sess, "Only the channel owner can do that.")
			return
		}
		if cmd.NeedsOp && role != store.RoleOwner && role != store.RoleOperator {
			sendError(sess, "You must be an operator or the owner to do that.")
			return
		}
	}

	// commandArgsWithRest lets handlers that want the raw remainder (topic,
	// admin message, ban reason) reconstruct it without worrying about
	// Fields() having already split on whitespace.
	c2 := rest
	lines := cmd.Fn(c, append(args, "\x00"+c2))
	for _, l := range lines {
		line := infoLine(l)
		sess.send(Outbound{Line: &line})
	}
}

func sendError(sess *Session, text string) {
	line := errorLine(text)
	sess.send(Outbound{Line: &line})
}

// restArg recovers the raw, unsplit remainder passed via the sentinel
// appended by Dispatch, so handlers needing free-text (topic/admin
// message/ban reason) don't have to rejoin Fields().
func restArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	last := args[len(args)-1]
	if strings.HasPrefix(last, "\x00") {
		return strings.TrimPrefix(last, "\x00")
	}
	return ""
}

// posArgs strips the sentinel rest-arg so handlers can treat args as plain
// positional tokens (e.g. args[0] == target user).
func posArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	return args[:len(args)-1]
}

func cmdHelp(c *CmdCtx, _ []string) []string {
	lines := []string{"Available commands:"}
	for _, name := range orderedNames {
		cmd := registry[name]
		lines = append(lines, fmt.Sprintf("  %-22s %s", cmd.Usage, cmd.Help))
	}
	return lines
}

func cmdNick(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /nick <name>"}
	}
	nick := pos[0]
	if !nickRe.MatchString(nick) {
		return []string{"Nicknames must be 2-24 characters: letters, numbers, dash, underscore."}
	}
	old := c.Sess.Nick()
	c.Sess.SetNick(nick)
	if err := c.Store.SetNickname(c.ctx, c.Sess.FP, nick); err != nil {
		return []string{"Failed to save nickname (it will reset next connection)."}
	}
	if room := c.Sess.CurrentRoom(); room != nil {
		room.Send(evSystem{text: fmt.Sprintf("*** %s is now known as %s ***", old, nick), persist: true})
	}
	return nil
}

func cmdList(c *CmdCtx, _ []string) []string {
	chans, err := c.Hub.ListChannels(c.ctx)
	if err != nil {
		return []string{"Failed to list channels."}
	}
	lines := []string{"Channels:"}
	for _, ch := range chans {
		lock := "unlocked"
		if ch.Locked {
			lock = "locked"
		}
		topic := ch.Topic
		if topic == "" {
			topic = "(no topic)"
		}
		lines = append(lines, fmt.Sprintf("  #%-20s %3d users  [%s]  %s", ch.Name, ch.Members, lock, topic))
	}
	return lines
}

func validateChannelName(name string) error {
	if strings.EqualFold(name, store.MainChannelName) {
		return fmt.Errorf("that name is reserved")
	}
	if !channelNameRe.MatchString(name) {
		return fmt.Errorf("channel names must be 2-24 characters: letters, numbers, dash, underscore")
	}
	return nil
}

func cmdCreate(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /create <name>"}
	}
	name := pos[0]
	if err := validateChannelName(name); err != nil {
		return []string{err.Error()}
	}
	if _, err := c.Store.CreateChannel(c.ctx, name, c.Sess.FP); err != nil {
		if err == store.ErrChannelExists {
			return []string{fmt.Sprintf("Channel #%s already exists. Use /join %s instead.", name, name)}
		}
		return []string{"Failed to create channel."}
	}
	return joinByName(c, name, true)
}

func cmdJoin(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /join <name>"}
	}
	return joinByName(c, pos[0], false)
}

func joinByName(c *CmdCtx, name string, justCreated bool) []string {
	ch, ok, err := c.Store.GetChannelByName(c.ctx, name)
	if err != nil {
		return []string{"Internal error looking up channel."}
	}
	if !ok {
		if err := validateChannelName(name); err != nil {
			return []string{err.Error()}
		}
		ch, err = c.Store.CreateChannel(c.ctx, name, c.Sess.FP)
		if err != nil {
			return []string{"Failed to create channel."}
		}
		justCreated = true
	}

	if !justCreated {
		banned, err := c.Store.IsBanned(c.ctx, ch.ID, c.Sess.FP, c.Sess.IP)
		if err != nil {
			return []string{"Internal error checking ban list."}
		}
		if banned {
			return []string{fmt.Sprintf("You are banned from #%s.", ch.Name)}
		}
		if ch.Locked {
			role, _, _ := c.Store.IsOwnerOrOp(c.ctx, ch.ID, c.Sess.FP)
			if role != store.RoleOwner && role != store.RoleOperator {
				return []string{fmt.Sprintf("#%s is locked.", ch.Name)}
			}
		}
	}

	oldRoom := c.Sess.CurrentRoom()
	newRoom := c.Hub.GetOrLoadRoom(c.ctx, ch)
	if oldRoom != nil && oldRoom != newRoom {
		oldRoom.Send(evPart{sess: c.Sess})
	}
	if oldRoom != newRoom {
		newRoom.Send(evJoin{sess: c.Sess})
	}
	return nil
}

func cmdLeave(c *CmdCtx, _ []string) []string {
	room := c.Sess.CurrentRoom()
	if room == nil || room == c.Hub.Main() {
		return []string{"You're already in #main."}
	}
	room.Send(evPart{sess: c.Sess})
	c.Hub.Main().Send(evJoin{sess: c.Sess})
	return nil
}

func cmdWho(c *CmdCtx, _ []string) []string {
	room := c.Sess.CurrentRoom()
	if room == nil {
		room = c.Hub.Main()
	}
	names := room.Who()
	sort.Strings(names)
	lines := []string{fmt.Sprintf("Users in #%s:", room.Name)}
	for _, n := range names {
		lines = append(lines, "  "+n)
	}
	return lines
}

func cmdTopic(c *CmdCtx, args []string) []string {
	topic := restArg(args)
	if topic == "" {
		return []string{"Usage: /topic <text>"}
	}
	c.Sess.CurrentRoom().Send(evSetTopic{topic: topic, by: c.Sess})
	return nil
}

func cmdAnnounce(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) != 1 || (pos[0] != "on" && pos[0] != "off") {
		return []string{"Usage: /announce on|off"}
	}
	c.Sess.CurrentRoom().Send(evSetAnnounce{announce: pos[0] == "on", by: c.Sess})
	return nil
}

// findLiveMember returns the connected *Session in room matching nick, if any.
func findLiveMember(room *Room, nick string) *Session {
	respond := make(chan []*Session, 1)
	room.Send(evFindMember{nick: nick, respond: respond})
	found := <-respond
	if len(found) == 0 {
		return nil
	}
	return found[0]
}

func cmdKick(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) < 1 {
		return []string{"Usage: /kick <user>"}
	}
	nick := pos[0]
	room := c.Sess.CurrentRoom()

	ownerRole, _, _ := c.Store.IsOwnerOrOp(c.ctx, room.ID, c.Sess.FP)
	target := findLiveMember(room, nick)
	if target == nil {
		return []string{fmt.Sprintf("%s is not in #%s.", nick, room.Name)}
	}
	targetRole, _, _ := c.Store.IsOwnerOrOp(c.ctx, room.ID, target.FP)
	if targetRole == store.RoleOwner && ownerRole != store.RoleOwner {
		return []string{"Operators cannot kick the channel owner."}
	}
	broadcastText := fmt.Sprintf("*** %s was kicked by %s ***", target.Nick(), c.Sess.Nick())
	targetText := fmt.Sprintf("You were kicked from #%s by %s.", room.Name, c.Sess.Nick())
	room.Send(evForceRemove{target: target, broadcastText: broadcastText, targetText: targetText})
	return nil
}

func cmdBan(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) < 1 {
		return []string{"Usage: /ban <user> [reason]"}
	}
	nick := pos[0]
	reason := strings.TrimSpace(strings.TrimPrefix(restArg(args), nick))
	room := c.Sess.CurrentRoom()

	ownerRole, _, _ := c.Store.IsOwnerOrOp(c.ctx, room.ID, c.Sess.FP)
	target := findLiveMember(room, nick)

	var fp, ip string
	if target != nil {
		targetRole, _, _ := c.Store.IsOwnerOrOp(c.ctx, room.ID, target.FP)
		if targetRole == store.RoleOwner && ownerRole != store.RoleOwner {
			return []string{"Operators cannot ban the channel owner."}
		}
		fp, ip = target.FP, target.IP
	} else {
		var ok bool
		var err error
		fp, ok, err = c.Store.FindFingerprintByNickname(c.ctx, nick)
		if err != nil || !ok {
			return []string{fmt.Sprintf("No such user %q.", nick)}
		}
	}

	if err := c.Store.AddBan(c.ctx, room.ID, fp, ip, c.Sess.Nick(), reason); err != nil {
		return []string{"Failed to add ban."}
	}

	if target != nil {
		broadcastText := fmt.Sprintf("*** %s was banned by %s ***", target.Nick(), c.Sess.Nick())
		targetText := fmt.Sprintf("You were banned from #%s by %s.", room.Name, c.Sess.Nick())
		room.Send(evForceRemove{target: target, broadcastText: broadcastText, targetText: targetText})
	} else {
		room.Send(evSystem{text: fmt.Sprintf("*** %s was banned by %s ***", nick, c.Sess.Nick()), persist: true})
	}
	return nil
}

func cmdUnban(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /unban <user>"}
	}
	nick := pos[0]
	room := c.Sess.CurrentRoom()
	fp, ok, err := c.Store.FindFingerprintByNickname(c.ctx, nick)
	if err != nil || !ok {
		return []string{fmt.Sprintf("No such user %q.", nick)}
	}
	if err := c.Store.RemoveBan(c.ctx, room.ID, fp); err != nil {
		return []string{"Failed to remove ban."}
	}
	room.Send(evSystem{text: fmt.Sprintf("*** %s was unbanned by %s ***", nick, c.Sess.Nick()), persist: true})
	return nil
}

func cmdOp(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /op <user>"}
	}
	nick := pos[0]
	room := c.Sess.CurrentRoom()
	target := findLiveMember(room, nick)
	var fp string
	if target != nil {
		fp = target.FP
	} else {
		var ok bool
		var err error
		fp, ok, err = c.Store.FindFingerprintByNickname(c.ctx, nick)
		if err != nil || !ok {
			return []string{fmt.Sprintf("No such user %q.", nick)}
		}
	}
	if err := c.Store.SetOperator(c.ctx, room.ID, fp, store.RoleOperator); err != nil {
		return []string{"Failed to grant operator status."}
	}
	room.Send(evSystem{text: fmt.Sprintf("*** %s was made an operator by %s ***", nick, c.Sess.Nick()), persist: true})
	return nil
}

func cmdDeop(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /deop <user>"}
	}
	nick := pos[0]
	room := c.Sess.CurrentRoom()
	fp, ok, err := c.Store.FindFingerprintByNickname(c.ctx, nick)
	if err != nil || !ok {
		return []string{fmt.Sprintf("No such user %q.", nick)}
	}
	if err := c.Store.RemoveOperator(c.ctx, room.ID, fp); err != nil {
		return []string{"Failed to revoke operator status."}
	}
	room.Send(evSystem{text: fmt.Sprintf("*** %s is no longer an operator (by %s) ***", nick, c.Sess.Nick()), persist: true})
	return nil
}

func cmdLock(c *CmdCtx, _ []string) []string {
	c.Sess.CurrentRoom().Send(evSetLocked{locked: true, by: c.Sess})
	return nil
}

func cmdUnlock(c *CmdCtx, _ []string) []string {
	c.Sess.CurrentRoom().Send(evSetLocked{locked: false, by: c.Sess})
	return nil
}

func cmdAdmin(c *CmdCtx, args []string) []string {
	msg := restArg(args)
	if msg == "" {
		return []string{"Usage: /admin <message describing the problem>"}
	}
	room := c.Sess.CurrentRoom()
	roomName := store.MainChannelName
	if room != nil {
		roomName = room.Name
	}
	delivered := c.Hub.NotifyAdmins(fmt.Sprintf("[ADMIN ALERT] %s in #%s: %s", c.Sess.Nick(), roomName, msg))
	_ = c.Store.AddAdminAlert(c.ctx, store.AdminAlert{
		ReporterFP: c.Sess.FP, ReporterNick: c.Sess.Nick(), ChannelName: roomName, Message: msg, Delivered: delivered,
	})
	if delivered {
		return []string{"Your report has been sent to an administrator."}
	}
	return []string{"No administrator is currently online; your report has been logged."}
}

func cmdQuit(c *CmdCtx, _ []string) []string {
	c.Sess.send(Outbound{Disconnect: true})
	return nil
}
