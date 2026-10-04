package bot

import (
	"fmt"
	"strconv"
	"time"
)

func (b *Bot) tracked(p Person) bool { return !p.Anon && !b.st.isOptedOut(p.FP) }

func (b *Bot) busy(humans int) bool { return humans >= b.cfg.BusyThreshold }

func (b *Bot) humanActivity() {
	b.lastActivity = b.now()
	b.humanSince = true
}

func contains(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}

// daysBetween counts calendar days (in the configured zone) from a to b.
func (b *Bot) daysBetween(a, z time.Time) int {
	y1, m1, d1 := a.In(b.cfg.loc).Date()
	y2, m2, d2 := z.In(b.cfg.loc).Date()
	t1 := time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)
	return int(t2.Sub(t1).Hours() / 24)
}

// candidate is a possible public remark about a login; commit (optional)
// records that it was actually used.
type candidate struct {
	text   string
	commit func()
}

// onLogin makes AT MOST ONE public comment about the person who just
// connected, choosing the most notable: caller milestone, anniversary, visit
// milestone, long absence, node-count record, then (if none) a personal
// greeting, a time-of-day remark in place of a plain "welcome back", and
// finally the plain entrance line. Private extras (keyless advice, delivered
// !tell messages) go by PM and do not count.
func (b *Bot) onLogin(p Person) {
	now := b.now()
	b.humanActivity()
	calls := b.st.incr("calls")
	humans := b.host.OnlineCount()
	busy := b.busy(humans)
	v := b.vars(p)
	speakNormal := !busy || b.rng.Float64() < b.cfg.BusyGreetProbability

	var specials []candidate // in priority order
	var greeting string
	var returning bool // greeting is a plain "welcome back" a time-of-day remark may replace
	var tellsFor string
	var private []string // for this user only, sent as PMs after the public line

	if contains(b.cfg.CallerMilestones, calls) {
		specials = append(specials, candidate{text: b.text("milestone_caller", b.vars(p, "n", strconv.Itoa(calls)))})
	}
	switch {
	case p.Anon:
		// The "you have no key" advice is nobody else's business: PM it.
		private = append(private, b.text("greet_anon", v))
	case b.st.isOptedOut(p.FP):
		// untracked by choice: nothing beyond the entrance line
	default:
		prev, existed, cur := b.st.recordVisit(p.FP, p.Nick, now)
		tellsFor = p.FP
		if years := int(now.Sub(cur.FirstSeen) / (365 * 24 * time.Hour)); years >= 1 && years > cur.Anniv {
			specials = append(specials, candidate{
				text:   b.text("anniversary", b.vars(p, "years", strconv.Itoa(years))),
				commit: func() { b.st.setAnniv(p.FP, years) }, // only once it has actually been announced
			})
		}
		if contains(b.cfg.VisitMilestones, cur.Visits) {
			specials = append(specials, candidate{text: b.text("milestone_visit", b.vars(p, "n", strconv.Itoa(cur.Visits)))})
		}
		if !existed {
			if speakNormal {
				greeting = b.text("greet_first", v)
			}
		} else {
			gap := now.Sub(prev.LastSeen)
			days := b.daysBetween(prev.LastSeen, now)
			dv := b.vars(p, "days", strconv.Itoa(days))
			switch {
			case gap >= 365*24*time.Hour:
				specials = append(specials, candidate{text: b.text("absence_365", dv)})
			case gap >= 90*24*time.Hour:
				specials = append(specials, candidate{text: b.text("absence_90", dv)})
			case gap >= 30*24*time.Hour:
				specials = append(specials, candidate{text: b.text("absence_30", dv)})
			case !speakNormal:
			case gap < time.Duration(b.cfg.RapidReconnectSeconds)*time.Second:
				greeting = b.text("greet_rapid", v)
			case days >= 1:
				greeting, returning = b.text("greet_returning", dv), true
			default:
				greeting, returning = b.text("greet_returning_today", v), true
			}
		}
	}
	if humans > b.nodeRecord {
		if humans >= 3 && b.nodeRecord > 0 {
			specials = append(specials, candidate{text: b.text("node_record", b.vars(p, "n", strconv.Itoa(humans)))})
		}
		b.nodeRecord = humans
		b.st.set("node_record", strconv.Itoa(humans))
	}

	var line string
	switch {
	case len(specials) > 0:
		line = specials[0].text
		if specials[0].commit != nil {
			specials[0].commit()
		}
	case greeting != "":
		line = greeting
		if returning && !busy && b.rng.Float64() < b.cfg.RemarkProbability {
			if r := b.timeRemark(now, v); r != "" {
				line = r
			}
		}
	case speakNormal:
		line = b.text("login", v)
	}
	b.say(kindEvent, line) // say() skips an empty line
	for _, t := range private {
		b.pm(p.Nick, t)
	}

	if tellsFor != "" {
		for _, t := range b.st.takeTells(tellsFor) {
			ago := humanAgo(now.Sub(t.At))
			b.pm(p.Nick, b.text("tell_deliver", b.vars(p, "from", clean(t.FromNick, 24), "ago", ago, "msg", t.Body)))
		}
	}
}

func (b *Bot) timeRemark(now time.Time, v map[string]string) string {
	local := now.In(b.cfg.loc)
	switch h := local.Hour(); {
	case h < 5:
		return b.text("remark_late", v)
	case h < 7:
		return b.text("remark_early", v)
	}
	if d := local.Weekday(); d == time.Saturday || d == time.Sunday {
		return b.text("remark_weekend", v)
	}
	return ""
}

func (b *Bot) onLogoff(p Person) {
	if b.tracked(p) {
		b.st.touchLastSeen(p.FP, b.now())
	}
	humans := b.host.OnlineCount()
	if b.busy(humans) && b.rng.Float64() >= b.cfg.BusyGreetProbability {
		return
	}
	b.say(kindEvent, b.text("logoff", b.vars(p)))
}

func (b *Bot) onChat(p Person, body string) {
	body = clean(body, 400)
	if body == "" {
		return
	}
	if b.curRoom == "" { // #main: activity tracking and the first-message reaction
		b.humanActivity()
		humans := b.host.OnlineCount()
		if b.tracked(p) {
			b.st.setNick(p.FP, p.Nick)
			if b.st.bumpMessages(p.FP) == 1 && !b.busy(humans) {
				b.say(kindEvent, b.text("first_message", b.vars(p)))
			}
		}
	}
	// In every channel, !commands and being addressed work the same.

	addressed, text := b.parseAddress(body)
	switch {
	case len(text) > 0 && text[0] == '!':
		b.command(p, text, addressed)
	case addressed:
		b.addressed(p, text)
	}
}

// Tick runs the time-driven rules: trivia expiry, scheduled posts, and the
// idle/lonely timers.
func (b *Bot) Tick() {
	now := b.now()
	b.checkTrivia(now)
	b.scheduled(now)

	humans := b.host.OnlineCount()
	if humans == 0 || !b.humanSince || b.busy(humans) {
		return
	}
	quiet := now.Sub(b.lastActivity)
	cooldown := time.Duration(b.cfg.LonelyCooldownMinutes) * time.Minute
	switch {
	case humans == 1 && quiet >= time.Duration(b.cfg.LonelyMinutes)*time.Minute &&
		(b.lastLonely.IsZero() || now.Sub(b.lastLonely) >= cooldown):
		b.lastLonely = now
		b.humanSince = false
		b.say(kindPlain, b.text("lonely", b.vars(Person{})))
	case quiet >= time.Duration(b.cfg.IdleMinutes)*time.Minute:
		b.humanSince = false // never twice in a row without human activity
		b.postIdle(now)
	}
}

func (b *Bot) scheduled(now time.Time) {
	local := now.In(b.cfg.loc)
	today := local.Format("2006-01-02")
	run := func(name, hhmm string, f func()) {
		t, _ := time.Parse("15:04", hhmm)
		at := time.Date(local.Year(), local.Month(), local.Day(), t.Hour(), t.Minute(), 0, 0, b.cfg.loc)
		if !local.Before(at) && local.Sub(at) < time.Hour && b.st.get("last_"+name) != today {
			b.st.set("last_"+name, today)
			f()
		}
	}
	run("quote", b.cfg.DailyQuoteTime, func() {
		q := b.c.Quotes[b.pk.listIndex("quote", len(b.c.Quotes), 5)]
		b.say(kindAnswer, b.text("quote_intro", b.vars(Person{})), fmt.Sprintf("\"%s\" -- %s", q.Text, q.By))
	})
	run("maintenance", b.cfg.MaintenanceTime, func() {
		b.say(kindEvent, b.text("maintenance", b.vars(Person{})))
	})
}

// postIdle posts the next item of rotating content.
func (b *Bot) postIdle(now time.Time) {
	kinds := []string{"fortune", "oneliner", "history", "trivia"}
	start := b.st.getInt("idle_idx")
	for i := 0; i < len(kinds); i++ {
		k := kinds[(start+i)%len(kinds)]
		if k == "trivia" && b.trivia["main"] != nil {
			continue
		}
		b.st.set("idle_idx", strconv.Itoa((start+i+1)%len(kinds)))
		intro := b.text("idle_intro", b.vars(Person{}))
		switch k {
		case "fortune":
			b.say(kindAnswer, intro, b.c.Fortunes[b.pk.listIndex("fortune", len(b.c.Fortunes), 8)])
		case "oneliner":
			b.say(kindAnswer, intro, b.c.Oneliners[b.pk.listIndex("oneliner", len(b.c.Oneliners), 6)])
		case "history":
			b.say(kindAnswer, intro, b.historyLine(now))
		case "trivia":
			b.say(kindAnswer, intro)
			b.startTrivia(now)
		}
		return
	}
}

// historyLine prefers an entry for today's date, else any entry.
func (b *Bot) historyLine(now time.Time) string {
	md := now.In(b.cfg.loc).Format("01-02")
	var todays []HistoryItem
	for _, h := range b.c.History {
		if h.Date == md {
			todays = append(todays, h)
		}
	}
	if len(todays) > 0 {
		h := todays[b.rng.IntN(len(todays))]
		return "This day in computing history: " + h.Text
	}
	h := b.c.History[b.pk.listIndex("history", len(b.c.History), 6)]
	t, _ := time.Parse("01-02", h.Date)
	return fmt.Sprintf("On %s in computing history: %s", t.Format("2 January"), h.Text)
}

func humanAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "moments ago"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
