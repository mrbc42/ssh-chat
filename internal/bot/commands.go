package bot

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
)

// parseAddress reports whether body addresses the bot ("@Nick", or a leading
// "Nick:" / "Nick,") and returns the text with the address removed.
func (b *Bot) parseAddress(body string) (bool, string) {
	low := strings.ToLower(body)
	nick := strings.ToLower(b.cfg.Nick)
	if i := strings.Index(low, "@"+nick); i >= 0 {
		rest := body[:i] + " " + body[i+1+len(nick):]
		return true, strings.TrimSpace(strings.Trim(rest, " :,"))
	}
	for _, sep := range []string{":", ","} {
		if strings.HasPrefix(low, nick+sep) {
			return true, strings.TrimSpace(body[len(nick)+1:])
		}
	}
	return false, strings.TrimSpace(body)
}

var rollRe = regexp.MustCompile(`^(\d{1,2})?d(\d{1,4})$`)

// command dispatches a "!name args" message. Unknown commands are ignored
// unless the bot was addressed directly.
func (b *Bot) command(p Person, text string, addressed bool) {
	name, args, _ := strings.Cut(strings.TrimPrefix(text, "!"), " ")
	name = strings.ToLower(name)
	args = strings.TrimSpace(args)
	h, ok := b.handlers()[name]
	if !ok {
		if addressed {
			b.addressed(p, text)
		}
		return
	}
	if !b.allow(p) {
		return
	}
	h(p, args)
}

// addressed handles a message aimed at the bot that is not a command: the
// FAQ first, then the in-persona "didn't catch that".
func (b *Bot) addressed(p Person, text string) {
	if !b.allow(p) {
		return
	}
	for _, e := range b.c.FAQ {
		for _, re := range e.res {
			if re.MatchString(text) {
				b.say(kindPlain, b.pk.poolText("faq:"+e.Patterns[0], e.Replies, b.vars(p)))
				return
			}
		}
	}
	b.logUnmatched(p, text)
	b.say(kindPlain, b.text("fallback", b.vars(p)))
}

// handlers maps each !command name to its implementation.
func (b *Bot) handlers() map[string]func(Person, string) {
	return map[string]func(Person, string){
		"help": b.cmdHelp, "time": b.cmdTime, "who": b.cmdWho, "seen": b.cmdSeen,
		"tell": b.cmdTell, "stats": b.cmdStats, "rules": b.cmdRules, "motd": b.cmdMotd,
		"roll": b.cmdRoll, "fortune": b.cmdFortune, "quote": b.cmdQuote, "trivia": b.cmdTrivia,
		"forget": b.cmdForget, "remember": b.cmdRemember, "ssh": b.cmdSSH,
	}
}

// commandHelp documents every !command, in display order. It feeds both Gus's
// own !help and the server's /help, so neither can drift from the other (a
// test checks it against handlers()).
var commandHelp = []struct{ name, usage, help string }{
	{"help", "!help", "List my commands."},
	{"time", "!time", "The board's time."},
	{"who", "!who", "Who is online right now."},
	{"seen", "!seen <user>", "When a user was last on."},
	{"tell", "!tell <user> <message>", "Leave a message, delivered at their next login (SSH-key users only)."},
	{"stats", "!stats", "Board statistics."},
	{"rules", "!rules", "The house rules."},
	{"motd", "!motd", "Show the message of the day. Admins: !motd set <text>, !motd clear."},
	{"roll", "!roll NdM", "Roll dice, e.g. !roll 2d6."},
	{"fortune", "!fortune", "A fortune."},
	{"quote", "!quote", "A quote."},
	{"trivia", "!trivia [answer|top]", "Ask a trivia question, answer it, or show the leaderboard."},
	{"forget", "!forget", "Erase everything I know about you and stop tracking you."},
	{"remember", "!remember", "Start tracking you again after !forget."},
	{"ssh", "!ssh", "Step-by-step guide to making an SSH key so you keep your name."},
}

// HelpLines is the section the server's /help shows for the bot's commands.
func HelpLines(nick string) []string {
	lines := []string{nick + " commands (they work in every channel):"}
	for _, c := range commandHelp {
		lines = append(lines, fmt.Sprintf("  %-22s %s", c.usage, c.help))
	}
	return lines
}

func (b *Bot) cmdHelp(p Person, _ string) {
	var usages []string
	for _, c := range commandHelp {
		usages = append(usages, c.usage)
	}
	b.say(kindPlain, b.text("help_intro", b.vars(p)), strings.Join(usages, "  "))
}

func (b *Bot) cmdTime(p Person, _ string) {
	t := b.now().In(b.cfg.loc)
	b.say(kindPlain, b.text("time_reply", b.vars(p, "time", t.Format("Mon 2 Jan 15:04 MST"))))
}

func (b *Bot) cmdWho(p Person, _ string) {
	var names []string
	for _, q := range b.host.Online() {
		names = append(names, clean(q.Nick, 24))
	}
	if len(names) == 0 {
		b.say(kindPlain, b.text("who_empty", b.vars(p)))
		return
	}
	b.say(kindPlain, b.text("who_reply", b.vars(p, "count", strconv.Itoa(len(names)), "list", strings.Join(names, ", "))))
}

func (b *Bot) cmdSeen(p Person, args string) {
	target := clean(args, 24)
	if i := strings.IndexByte(target, ' '); i > 0 {
		target = target[:i]
	}
	if target == "" {
		b.say(kindPlain, b.text("seen_usage", b.vars(p)))
		return
	}
	for _, q := range b.host.Online() {
		if strings.EqualFold(q.Nick, target) {
			b.say(kindPlain, b.text("seen_online", b.vars(p, "target", clean(q.Nick, 24))))
			return
		}
	}
	if u, ok := b.st.userByNick(target); ok {
		b.say(kindPlain, b.text("seen_ago", b.vars(p, "target", clean(u.LastNick, 24),
			"ago", humanAgo(b.now().Sub(u.LastSeen)), "date", u.LastSeen.In(b.cfg.loc).Format("2 Jan 2006"))))
		return
	}
	b.say(kindPlain, b.text("seen_unknown", b.vars(p, "target", target)))
}

func (b *Bot) cmdTell(p Person, args string) {
	if !b.tracked(p) {
		b.pm(p.Nick, b.text("tell_need_key", b.vars(p))) // about their key: keep it private
		return
	}
	targetRaw, msgRaw, _ := strings.Cut(args, " ")
	target, msg := clean(targetRaw, 24), clean(msgRaw, b.cfg.TellMaxLen)
	if target == "" || msg == "" {
		b.say(kindPlain, b.text("tell_usage", b.vars(p)))
		return
	}
	tv := b.vars(p, "target", target)
	if strings.EqualFold(target, b.cfg.Nick) {
		b.say(kindPlain, b.text("tell_self", tv))
		return
	}
	var toFP string
	for _, q := range b.host.Online() {
		if !strings.EqualFold(q.Nick, target) {
			continue
		}
		switch {
		case q.FP == p.FP:
			b.say(kindPlain, b.text("tell_self", tv))
		case q.Anon:
			b.say(kindPlain, b.text("tell_unknown", tv))
		default:
			b.say(kindPlain, b.text("tell_online", tv)) // they're here: no need to queue
		}
		return
	}
	u, ok := b.st.userByNick(target)
	switch {
	case !ok:
		b.say(kindPlain, b.text("tell_unknown", tv))
		return
	case u.FP == p.FP:
		b.say(kindPlain, b.text("tell_self", tv))
		return
	case b.st.countTellsTo(u.FP) >= b.cfg.TellMaxPerRecipient:
		b.say(kindPlain, b.text("tell_full", tv))
		return
	case b.st.countTellsFrom(p.FP) >= b.cfg.TellMaxPerSender:
		b.say(kindPlain, b.text("tell_limit", tv))
		return
	}
	toFP = u.FP
	if err := b.st.addTell(toFP, p.FP, p.Nick, msg, b.now()); err != nil {
		return
	}
	b.say(kindPlain, b.text("tell_stored", tv))
}

func (b *Bot) cmdStats(p Person, _ string) {
	b.say(kindPlain, b.text("stats_reply", b.vars(p,
		"calls", strconv.Itoa(b.st.getInt("calls")),
		"users", strconv.Itoa(b.st.countUsers()),
		"record", strconv.Itoa(b.nodeRecord),
		"trivia", strconv.Itoa(b.st.getInt("trivia_asked")))))
}

func (b *Bot) cmdRules(p Person, _ string) {
	b.say(kindPlain, append([]string{b.text("rules_intro", b.vars(p))}, b.c.Rules...)...)
}

// currentMotd is the MOTD an admin has set, or the data-file default if none
// has ever been set. An admin-cleared MOTD is "" (nothing is shown).
func (b *Bot) currentMotd() string {
	if b.st.get("motd_set") == "1" {
		return b.st.get("motd")
	}
	return b.c.Motd
}

const maxMotdChars = 300

// cmdMotd shows the message of the day; "set <text>" and "clear" are for server
// administrators only. Authority comes from the sender's SSH key fingerprint,
// which the server verified at login and which chat text cannot forge; a
// keyless user's identity is random and can never be an admin.
func (b *Bot) cmdMotd(p Person, args string) {
	sub, rest, _ := strings.Cut(strings.TrimSpace(args), " ")
	switch strings.ToLower(sub) {
	case "set", "clear":
		if p.Anon || !b.host.IsAdmin(p.FP) {
			b.say(kindPlain, b.text("motd_denied", b.vars(p)))
			return
		}
		if strings.EqualFold(sub, "clear") {
			b.st.set("motd", "")
			b.st.set("motd_set", "1")
			log.Printf("bot: MOTD cleared by %s (%s)", clean(p.Nick, 24), p.FP)
			b.say(kindPlain, b.text("motd_cleared", b.vars(p)))
			return
		}
		text := clean(rest, maxMotdChars)
		if text == "" {
			b.say(kindPlain, b.text("motd_usage", b.vars(p)))
			return
		}
		b.st.set("motd", text)
		b.st.set("motd_set", "1")
		log.Printf("bot: MOTD set by %s (%s)", clean(p.Nick, 24), p.FP)
		b.say(kindPlain, b.text("motd_set", b.vars(p)))
		return
	}
	// Shown to the asker only, as the same boxed block they get at login.
	if text := b.currentMotd(); text != "" {
		b.emit([]outLine{{pmTo: p.Nick, motd: true, text: text}})
	} else {
		b.pm(p.Nick, b.text("motd_none", b.vars(p)))
	}
}

func (b *Bot) cmdRoll(p Person, args string) {
	m := rollRe.FindStringSubmatch(strings.ToLower(strings.ReplaceAll(args, " ", "")))
	if m == nil {
		b.say(kindPlain, b.text("roll_bad", b.vars(p)))
		return
	}
	n := 1
	if m[1] != "" {
		n, _ = strconv.Atoi(m[1])
	}
	sides, _ := strconv.Atoi(m[2])
	if n < 1 || n > 20 || sides < 2 || sides > 1000 {
		b.say(kindPlain, b.text("roll_bad", b.vars(p)))
		return
	}
	total := 0
	rolls := make([]string, n)
	for i := range rolls {
		r := 1 + b.rng.IntN(sides)
		total += r
		rolls[i] = strconv.Itoa(r)
	}
	b.say(kindPlain, b.text("roll_result", b.vars(p, "expr", fmt.Sprintf("%dd%d", n, sides),
		"total", strconv.Itoa(total), "rolls", strings.Join(rolls, "+"))))
}

func (b *Bot) cmdFortune(_ Person, _ string) {
	b.say(kindAnswer, b.c.Fortunes[b.pk.listIndex("fortune", len(b.c.Fortunes), 8)])
}

func (b *Bot) cmdQuote(_ Person, _ string) {
	q := b.c.Quotes[b.pk.listIndex("quote", len(b.c.Quotes), 5)]
	b.say(kindAnswer, fmt.Sprintf("\"%s\" -- %s", q.Text, q.By))
}

func (b *Bot) cmdForget(p Person, _ string) {
	if p.Anon {
		b.pm(p.Nick, b.text("forget_anon", b.vars(p)))
		return
	}
	b.st.forget(p.FP)
	b.say(kindPlain, b.text("forget_done", b.vars(p)))
}

func (b *Bot) cmdRemember(p Person, _ string) {
	if !p.Anon {
		b.st.remember(p.FP)
	}
	b.say(kindPlain, b.text("remember_done", b.vars(p)))
}

// cmdSSH shows the SSH-key guide to the person who asked, privately and as
// plain lines, with this server's address and port filled into the examples.
func (b *Bot) cmdSSH(p Person, _ string) {
	b.emit([]outLine{{pmTo: p.Nick, guide: b.sshGuide()}})
}

// sshGuide is the guide text with {host} and {port} filled in. Without a
// configured address the examples say <server-address>, and the guide then
// tells the reader to use the address they already connect with.
func (b *Bot) sshGuide() []string {
	host, port := b.publicHost, b.publicPort
	if port == "" {
		port = "2222"
	}
	vars := map[string]string{"host": host, "port": port}
	if host == "" {
		vars["host"] = "<server-address>"
	}
	lines := make([]string, 0, len(b.c.SSHHelp)+1)
	for _, l := range b.c.SSHHelp {
		lines = append(lines, fill(l, vars))
	}
	if host == "" {
		lines = append(lines, "(Where it says <server-address>, use the address you normally connect to this server with.)")
	}
	return lines
}
