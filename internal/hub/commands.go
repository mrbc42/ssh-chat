package hub

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	AdminOnly bool // server administrator required (hidden from /help for everyone else)
	Fn        func(c *CmdCtx, args []string) []string
}

var registry = map[string]*Command{}
var orderedNames []string

// CommandNames returns every registered command name, including aliases,
// sorted — used by the UI for slash-command tab completion.
func CommandNames() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

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
		Name: "msg", Aliases: []string{"pm", "whisper", "w"}, Usage: "/msg <user> <message>", Help: "Send a private message to a user, anywhere on the server.",
		Fn: cmdMsg,
	})
	register(&Command{
		Name: "broadcast", Usage: "/broadcast <message>", Help: "(admin) Announce a message to every channel, e.g. for maintenance.",
		AdminOnly: true, Fn: cmdBroadcast,
	})
	register(&Command{
		Name: "roll", Usage: "/roll [sides]", Help: "Roll a die; defaults to 6 sides, up to 100.",
		Fn: cmdRoll,
	})
	register(&Command{
		Name: "flip", Usage: "/flip", Help: "Flip a coin: heads or tails.",
		Fn: cmdFlip,
	})
	register(&Command{
		Name: "afk", Usage: "/afk", Help: "Mark yourself away; cleared automatically on your next message.",
		Fn: cmdAfk,
	})
	register(&Command{
		Name: "me", Usage: "/me <action>", Help: "Emote, e.g. /me waves shows \"* nick waves\".",
		Fn: cmdMe,
	})
	register(&Command{
		Name: "time", Usage: "/time", Help: "Show how long you have been online.",
		Fn: cmdTime,
	})
	register(&Command{
		Name: "ignore", Usage: "/ignore [user]", Help: "Hide a user's chat, actions and PMs for this session; no argument lists who you ignore.",
		Fn: cmdIgnore,
	})
	register(&Command{
		Name: "unignore", Usage: "/unignore <user>", Help: "Stop ignoring a user.",
		Fn: cmdUnignore,
	})
	register(&Command{
		Name: "clear", Usage: "/clear", Help: "Clear your screen.",
		Fn: cmdClear,
	})
	register(&Command{
		Name: "seen", Usage: "/seen <user>", Help: "Show whether a user is online, or when they were last seen.",
		Fn: cmdSeen,
	})
	register(&Command{
		Name: "gban", Usage: "/gban <user> [reason]", Help: "(admin) Ban a user from the whole server (by key and IP) and disconnect them.",
		AdminOnly: true, Fn: cmdGban,
	})
	register(&Command{
		Name: "gunban", Usage: "/gunban <fingerprint|ip>", Help: "(admin) Lift a server-wide ban; see /gbans for the entries.",
		AdminOnly: true, Fn: cmdGunban,
	})
	register(&Command{
		Name: "gbans", Usage: "/gbans", Help: "(admin) List server-wide bans.",
		AdminOnly: true, Fn: cmdGbans,
	})
	register(&Command{
		Name: "delroom", Usage: "/delroom <channel>", Help: "(admin) Delete a channel; anyone inside is moved to #main.",
		AdminOnly: true, Fn: cmdDelroom,
	})
	register(&Command{
		Name: "adminlog", Usage: "/adminlog [count]", Help: "(admin) Show recent /admin reports (default 10, max 50).",
		AdminOnly: true, Fn: cmdAdminLog,
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

	// Any submitted line counts as activity and clears away status —
	// except /afk itself, which sets it right back; without this
	// exception, going away while already away would announce "has returned"
	// immediately followed by "is away" for the same keystroke.
	isAfkCmd := strings.EqualFold(strings.TrimSpace(line), "/afk")
	if wasAfk := sess.Touch(); wasAfk && !isAfkCmd {
		room := sess.CurrentRoom()
		if room == nil {
			room = h.Main()
		}
		room.Send(evSystem{text: fmt.Sprintf("*** %s has returned ***", sess.Nick()), persist: true})
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
	room.Send(evChat{sess: sess, body: h.mask(body)})
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

// canUse reports whether sess may run cmd right now, so /help lists only
// what the caller can actually do: admin commands for admins, and
// operator/owner commands only while in a channel where they hold that role.
func canUse(c *CmdCtx, cmd *Command) bool {
	if cmd.AdminOnly {
		return c.Hub.IsAdmin(c.Sess.FP)
	}
	if !cmd.NeedsOp && !cmd.OwnerOnly {
		return true
	}
	room := c.Sess.CurrentRoom()
	if room == nil || room == c.Hub.Main() {
		return false
	}
	role, _, err := c.Store.IsOwnerOrOp(c.ctx, room.ID, c.Sess.FP)
	if err != nil {
		return false
	}
	if cmd.OwnerOnly {
		return role == store.RoleOwner
	}
	return role == store.RoleOwner || role == store.RoleOperator
}

func cmdHelp(c *CmdCtx, _ []string) []string {
	lines := []string{"Available commands:"}
	for _, name := range orderedNames {
		cmd := registry[name]
		if !canUse(c, cmd) {
			continue
		}
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
	if old == nick {
		return []string{"That is already your nickname."}
	}
	if !c.Hub.NickAvailable(c.ctx, nick, c.Sess) {
		return []string{fmt.Sprintf("The nickname %q is already taken.", nick)}
	}
	c.Sess.SetNick(nick)
	if !strings.HasPrefix(c.Sess.FP, "anon-") {
		if err := c.Store.SetNickname(c.ctx, c.Sess.FP, nick); err != nil {
			return []string{"Failed to save nickname (it will reset next connection)."}
		}
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
		if oldRoom == c.Hub.Main() {
			// Leaving #main specifically to go to another channel gets its
			// own wording ("has joined another channel") rather than the
			// generic "has left #main".
			oldRoom.PartSwitching(c.Sess)
		} else {
			oldRoom.Part(c.Sess)
		}
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

func cmdMsg(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) < 1 {
		return []string{"Usage: /msg <user> <message>"}
	}
	target := pos[0]
	msg := strings.TrimSpace(strings.TrimPrefix(restArg(args), target))
	if msg == "" {
		return []string{"Usage: /msg <user> <message>"}
	}
	if strings.EqualFold(target, c.Sess.Nick()) {
		return []string{"You can't message yourself."}
	}
	targetSess := c.Hub.FindSessionByNick(target)
	if targetSess == nil {
		return []string{fmt.Sprintf("No such user %q is currently online.", target)}
	}
	if len(msg) > maxMessageLen {
		msg = msg[:maxMessageLen]
	}
	msg = c.Hub.mask(msg)

	// An ignoring recipient silently drops the PM; the sender still sees
	// their own copy so they aren't tipped off.
	if !targetSess.isIgnoring(c.Sess.Nick()) {
		inLine := pmLine(c.Sess.Nick(), "from", msg)
		targetSess.send(Outbound{Line: &inLine})
	}

	outLine := pmLine(targetSess.Nick(), "to", msg)
	c.Sess.send(Outbound{Line: &outLine})
	return nil
}

func cmdBroadcast(c *CmdCtx, args []string) []string {
	if !c.Hub.IsAdmin(c.Sess.FP) {
		return []string{"Only a server administrator can do that."}
	}
	msg := restArg(args)
	if msg == "" {
		return []string{"Usage: /broadcast <message>"}
	}
	c.Hub.AdminBroadcast(fmt.Sprintf("[ADMIN] %s", msg))
	return []string{"Broadcast sent to every channel."}
}

const maxDiceSides = 100

func cmdRoll(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	sides := 6
	if len(pos) >= 1 {
		n, err := strconv.Atoi(pos[0])
		if err != nil || n < 2 || n > maxDiceSides {
			return []string{fmt.Sprintf("Usage: /roll [sides] — sides must be a number from 2 to %d.", maxDiceSides)}
		}
		sides = n
	}
	n := randRange(1, sides)
	room := c.Sess.CurrentRoom()
	if room == nil {
		room = c.Hub.Main()
	}
	room.Send(evSystem{text: fmt.Sprintf("*** %s rolled a %d (1-%d) ***", c.Sess.Nick(), n, sides), persist: true})
	return nil
}

func cmdFlip(c *CmdCtx, _ []string) []string {
	side := "heads"
	if randRange(0, 1) == 1 {
		side = "tails"
	}
	room := c.Sess.CurrentRoom()
	if room == nil {
		room = c.Hub.Main()
	}
	room.Send(evSystem{text: fmt.Sprintf("*** %s flipped a coin: %s ***", c.Sess.Nick(), side), persist: true})
	return nil
}

func cmdAfk(c *CmdCtx, _ []string) []string {
	// HandleInput's Touch() already cleared any prior away status before
	// dispatching here (and suppressed its own "has returned"
	// announcement for this exact command), so this always marks away
	// fresh — no branching on prior state needed.
	c.Sess.SetAfk(true)
	room := c.Sess.CurrentRoom()
	if room == nil {
		room = c.Hub.Main()
	}
	room.Send(evSystem{text: fmt.Sprintf("*** %s is away ***", c.Sess.Nick()), persist: true})
	return nil
}

func cmdQuit(c *CmdCtx, _ []string) []string {
	c.Sess.send(Outbound{Disconnect: true})
	return nil
}

func cmdMe(c *CmdCtx, args []string) []string {
	body := strings.TrimSpace(restArg(args))
	if body == "" {
		return []string{"Usage: /me <action>"}
	}
	if !c.Sess.Allow() {
		return []string{"You're sending messages too fast. Slow down."}
	}
	if len(body) > maxMessageLen {
		body = body[:maxMessageLen]
	}
	room := c.Sess.CurrentRoom()
	if room == nil {
		room = c.Hub.Main()
	}
	room.Send(evAction{sess: c.Sess, body: c.Hub.mask(body)})
	return nil
}

func cmdTime(c *CmdCtx, _ []string) []string {
	return []string{fmt.Sprintf("You have been online for %s.", humanDuration(time.Since(c.Sess.ConnectedAt)))}
}

func cmdIgnore(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) == 0 {
		list := c.Sess.IgnoredNicks()
		if len(list) == 0 {
			return []string{"You are not ignoring anyone. Usage: /ignore <user>"}
		}
		return []string{"Ignoring: " + strings.Join(list, ", ")}
	}
	nick := pos[0]
	if strings.EqualFold(nick, c.Sess.Nick()) {
		return []string{"You can't ignore yourself."}
	}
	c.Sess.Ignore(nick)
	return []string{fmt.Sprintf("Ignoring %s for the rest of this session. Use /unignore %s to undo.", nick, nick)}
}

func cmdUnignore(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) == 0 {
		return []string{"Usage: /unignore <user>"}
	}
	if !c.Sess.Unignore(pos[0]) {
		return []string{fmt.Sprintf("You were not ignoring %s.", pos[0])}
	}
	return []string{fmt.Sprintf("No longer ignoring %s.", pos[0])}
}

func cmdClear(c *CmdCtx, _ []string) []string {
	c.Sess.send(Outbound{Clear: true})
	return nil
}

// humanDuration renders d like "2h 5m 9s", omitting leading zero units.
func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case h >= 24:
		return fmt.Sprintf("%dd %dh %dm", h/24, h%24, m)
	case h > 0:
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

func requireAdmin(c *CmdCtx) []string {
	if !c.Hub.IsAdmin(c.Sess.FP) {
		return []string{"Only a server administrator can do that."}
	}
	return nil
}

func cmdSeen(c *CmdCtx, args []string) []string {
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /seen <user>"}
	}
	nick := pos[0]
	if t := c.Hub.FindSessionByNick(nick); t != nil {
		msg := fmt.Sprintf("%s is online now (connected %s", t.Nick(), humanDuration(time.Since(t.ConnectedAt)))
		if t.IsAfk() {
			msg += ", away"
		} else if idle := t.IdleFor(); idle >= time.Minute {
			msg += ", idle " + humanDuration(idle)
		}
		return []string{msg + ")."}
	}
	at, ok, err := c.Store.LastSeenByNickname(c.ctx, nick)
	if err != nil {
		return []string{"Internal error looking up that user."}
	}
	if !ok {
		return []string{fmt.Sprintf("No record of %q.", nick)}
	}
	return []string{fmt.Sprintf("%s was last seen %s ago (%s).", nick, humanDuration(time.Since(at)), at.Format("2006-01-02 15:04"))}
}

func cmdGban(c *CmdCtx, args []string) []string {
	if msg := requireAdmin(c); msg != nil {
		return msg
	}
	pos := posArgs(args)
	if len(pos) < 1 {
		return []string{"Usage: /gban <user> [reason]"}
	}
	nick := pos[0]
	reason := strings.TrimSpace(strings.TrimPrefix(restArg(args), nick))

	var fp, ip string
	target := c.Hub.FindSessionByNick(nick)
	if target != nil {
		if target == c.Sess || c.Hub.IsAdmin(target.FP) {
			return []string{"You can't ban an administrator (or yourself)."}
		}
		fp, ip = target.FP, target.IP
	} else {
		var ok bool
		var err error
		fp, ok, err = c.Store.FindFingerprintByNickname(c.ctx, nick)
		if err != nil || !ok || strings.HasPrefix(fp, "anon-") {
			return []string{fmt.Sprintf("No such user %q (only keyed users can be banned while offline).", nick)}
		}
		if c.Hub.IsAdmin(fp) {
			return []string{"You can't ban an administrator."}
		}
	}
	// A keyless connection's fingerprint is random per connection, so only
	// its IP is worth banning.
	if strings.HasPrefix(fp, "anon-") {
		fp = ""
	}
	if fp == "" && ip == "" {
		return []string{"Nothing to ban: no key or address known for that user."}
	}
	if err := c.Store.AddGlobalBan(c.ctx, fp, ip, c.Sess.Nick(), reason); err != nil {
		return []string{"Failed to add ban."}
	}
	if target != nil {
		line := errorLine("You have been banned from this server.")
		target.send(Outbound{Line: &line})
		target.send(Outbound{Disconnect: true})
	}
	return []string{fmt.Sprintf("%s banned server-wide (key %s, address %s). Lift with /gunban.", nick, orDash(fp), orDash(ip))}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdGunban(c *CmdCtx, args []string) []string {
	if msg := requireAdmin(c); msg != nil {
		return msg
	}
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /gunban <fingerprint|ip>  (see /gbans)"}
	}
	key := pos[0]
	n, err := c.Store.RemoveGlobalBans(c.ctx, key)
	if err != nil {
		return []string{"Failed to lift ban."}
	}
	if n == 0 {
		// Allow a nickname too, resolved to its key.
		if fp, ok, _ := c.Store.FindFingerprintByNickname(c.ctx, key); ok {
			n, _ = c.Store.RemoveGlobalBans(c.ctx, fp)
		}
	}
	if n == 0 {
		return []string{fmt.Sprintf("No server-wide ban matches %q.", key)}
	}
	return []string{fmt.Sprintf("Lifted %d server-wide ban(s) for %s.", n, key)}
}

func cmdGbans(c *CmdCtx, _ []string) []string {
	if msg := requireAdmin(c); msg != nil {
		return msg
	}
	bans, err := c.Store.ListGlobalBans(c.ctx)
	if err != nil {
		return []string{"Internal error listing bans."}
	}
	if len(bans) == 0 {
		return []string{"No server-wide bans."}
	}
	lines := []string{"Server-wide bans:"}
	for _, b := range bans {
		lines = append(lines, fmt.Sprintf("  %s key=%s ip=%s by %s %s", b.CreatedAt.Format("2006-01-02"), orDash(b.FP), orDash(b.IP), b.BannedBy, b.Reason))
	}
	return lines
}

func cmdDelroom(c *CmdCtx, args []string) []string {
	if msg := requireAdmin(c); msg != nil {
		return msg
	}
	pos := posArgs(args)
	if len(pos) != 1 {
		return []string{"Usage: /delroom <channel>"}
	}
	name := strings.TrimPrefix(pos[0], "#")
	switch err := c.Hub.DeleteRoom(c.ctx, name); {
	case err == nil:
		return []string{fmt.Sprintf("Deleted #%s.", name)}
	case errors.Is(err, store.ErrChannelNotFound):
		return []string{fmt.Sprintf("No such channel #%s.", name)}
	default:
		return []string{err.Error()}
	}
}

func cmdAdminLog(c *CmdCtx, args []string) []string {
	if msg := requireAdmin(c); msg != nil {
		return msg
	}
	n := 10
	if pos := posArgs(args); len(pos) >= 1 {
		v, err := strconv.Atoi(pos[0])
		if err != nil || v < 1 || v > 50 {
			return []string{"Usage: /adminlog [count] — count from 1 to 50."}
		}
		n = v
	}
	alerts, err := c.Store.RecentAdminAlerts(c.ctx, n)
	if err != nil {
		return []string{"Internal error reading the admin log."}
	}
	if len(alerts) == 0 {
		return []string{"No admin reports logged."}
	}
	lines := []string{fmt.Sprintf("Last %d admin report(s), newest first:", len(alerts))}
	for _, a := range alerts {
		seen := "unseen"
		if a.Delivered {
			seen = "delivered live"
		}
		lines = append(lines, fmt.Sprintf("  %s %s in #%s (%s): %s", a.CreatedAt.Format("01-02 15:04"), a.ReporterNick, a.ChannelName, seen, a.Message))
	}
	return lines
}
