package bot

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type activeTrivia struct {
	q     TriviaQ
	asked time.Time
}

func (b *Bot) cmdTrivia(p Person, args string) {
	switch strings.ToLower(args) {
	case "":
		if b.trivia != nil {
			b.say(kindAnswer, b.text("trivia_already", b.vars(p)), "Q: "+b.trivia.q.Q)
			return
		}
		b.startTrivia(b.now())
	case "top":
		b.leaderboard(p)
	default:
		b.answerTrivia(p, args)
	}
}

func (b *Bot) startTrivia(now time.Time) {
	mem := len(b.c.Trivia) / 2
	q := b.c.Trivia[b.pk.listIndex("trivia", len(b.c.Trivia), mem)]
	b.trivia = &activeTrivia{q: q, asked: now}
	b.st.incr("trivia_asked")
	b.say(kindAnswer, b.text("trivia_ask", b.vars(Person{}, "seconds", strconv.Itoa(b.cfg.TriviaSeconds))), "Q: "+q.Q)
}

// checkTrivia reveals the answer once a question has gone unanswered too long.
func (b *Bot) checkTrivia(now time.Time) {
	if b.trivia == nil || now.Sub(b.trivia.asked) < time.Duration(b.cfg.TriviaSeconds)*time.Second {
		return
	}
	ans := b.trivia.q.A[0]
	b.trivia = nil
	b.say(kindAnswer, b.text("trivia_timeout", b.vars(Person{}, "answer", ans)))
}

func (b *Bot) answerTrivia(p Person, guess string) {
	if b.trivia == nil {
		b.say(kindPlain, b.text("trivia_none", b.vars(p)))
		return
	}
	g := normAnswer(guess)
	for _, a := range b.trivia.q.A {
		if g != "" && g == normAnswer(a) {
			elapsed := b.now().Sub(b.trivia.asked)
			pts := 1
			switch {
			case elapsed < 10*time.Second:
				pts = 3
			case elapsed < 30*time.Second:
				pts = 2
			}
			b.trivia = nil
			v := b.vars(p, "pts", strconv.Itoa(pts), "answer", a)
			b.say(kindAnswer, b.text("trivia_correct", v))
			if b.tracked(p) {
				b.st.addScore(p.FP, p.Nick, pts)
			} else {
				b.pm(p.Nick, b.text("trivia_anon", v))
			}
			return
		}
	}
	b.say(kindPlain, b.text("trivia_wrong", b.vars(p)))
}

func (b *Bot) leaderboard(p Person) {
	top := b.st.topScores(5)
	if len(top) == 0 {
		b.say(kindPlain, b.text("trivia_top_empty", b.vars(p)))
		return
	}
	lines := []string{b.text("trivia_top", b.vars(p))}
	for i, r := range top {
		lines = append(lines, fmt.Sprintf("%d. %s - %d pts (%d right)", i+1, clean(r.Nick, 24), r.Points, r.Correct))
	}
	b.say(kindAnswer, lines...)
}

// normAnswer lowercases, drops punctuation and a leading article, and
// collapses whitespace so "The  Zmodem!" matches "zmodem".
func normAnswer(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	f := strings.Fields(b.String())
	if len(f) > 1 && (f[0] == "the" || f[0] == "a" || f[0] == "an") {
		f = f[1:]
	}
	return strings.Join(f, " ")
}
