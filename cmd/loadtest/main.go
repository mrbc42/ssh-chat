// Command loadtest connects many anonymous (keyless) users to an ssh-chat
// server, has each of them chat like a normal conversation, and reports how
// long messages take to reach everyone else.
//
// Every message carries a token "(mSENDER.SEQ)". The sender records when it
// typed the message; every other client records when that token first
// appears on its screen. The difference is the delivery lag. All clients
// share one process clock, so no clock sync is needed.
package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

var tokenRe = regexp.MustCompile(`\(m(\d+)\.(\d+)\)`)

var phrases = []string{
	"morning all", "anyone around?", "just got in from work", "what a day", "has anyone seen the footy score",
	"I reckon it's going to rain", "ha, fair point", "no way", "yeah same here", "that's pretty funny",
	"I'm making a cuppa, want one?", "what's everyone up to tonight", "ugh, traffic was terrible",
	"did you try turning it off and on again", "lol", "I was thinking the same thing", "brb two minutes",
	"back", "any good shows on lately?", "I'm trying to fix my fence", "the weather's turned nasty",
	"you get a good signal out there?", "nice one", "wouldn't surprise me", "I've been on this board since the nineties",
	"who's cooking dinner?", "I could murder a pizza", "ok that makes sense", "hold on, phone's ringing",
	"never heard of it", "give it a go and see", "that's not how I remember it", "ha ha ha", "true story",
	"right, I'm off to bed soon", "what time is it over there?", "sounds like a plan", "cheers mate",
}

type sample struct {
	at  time.Time // when the message was sent
	lag time.Duration
}

type client struct {
	id        int
	sess      *ssh.Session
	in        io.WriteCloser
	connectMs float64
	readyAt   time.Time
	seen      map[string]bool
	mu        sync.Mutex
	dropped   bool // connection ended before the test did
	sawErr    map[string]bool
}

type results struct {
	mu       sync.Mutex
	sentAt   map[string]time.Time
	sentList []string       // order sent
	got      map[string]int // token -> receivers
	expected map[string]int // token -> clients ready & alive at send time
	lags     []sample
	selfLags []sample
}

var res = &results{sentAt: map[string]time.Time{}, got: map[string]int{}, expected: map[string]int{}}

func main() {
	addr := flag.String("addr", "127.0.0.1:2224", "server host:port")
	users := flag.Int("users", 50, "concurrent anonymous users")
	duration := flag.Duration("duration", 3*time.Minute, "how long the conversation runs once everyone is connected")
	ramp := flag.Duration("ramp", 25*time.Second, "spread connections over this long")
	minGap := flag.Duration("min", 4*time.Second, "shortest pause between one user's messages")
	maxGap := flag.Duration("max", 12*time.Second, "longest pause between one user's messages")
	seed := flag.Int64("seed", time.Now().UnixNano(), "random seed")
	flag.Parse()

	cfg := &ssh.ClientConfig{
		User:            "loadtest",
		Auth:            []ssh.AuthMethod{ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { return nil, nil })},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	clients := make([]*client, *users)
	var wg sync.WaitGroup
	fmt.Printf("connecting %d anonymous users to %s over %s ...\n", *users, *addr, *ramp)
	for i := range clients {
		c := &client{id: i, seen: map[string]bool{}, sawErr: map[string]bool{}}
		clients[i] = c
		wg.Add(1)
		go func(c *client) {
			defer wg.Done()
			time.Sleep(time.Duration(float64(*ramp) * float64(c.id) / float64(*users)))
			c.connect(*addr, cfg)
		}(c)
	}
	wg.Wait()

	ready := 0
	var connect []float64
	for _, c := range clients {
		if !c.readyAt.IsZero() {
			ready++
			connect = append(connect, c.connectMs)
		}
	}
	fmt.Printf("%d/%d users connected and seeing #main\n", ready, *users)
	if ready == 0 {
		os.Exit(1)
	}

	end := time.Now().Add(*duration)
	fmt.Printf("chatting for %s (each user speaks every %s-%s) ...\n", *duration, *minGap, *maxGap)
	var cw sync.WaitGroup
	for _, c := range clients {
		if c.readyAt.IsZero() {
			continue
		}
		cw.Add(1)
		go func(c *client) {
			defer cw.Done()
			r := rand.New(rand.NewSource(*seed + int64(c.id)))
			time.Sleep(time.Duration(r.Int63n(int64(*maxGap)))) // de-synchronise
			for seq := 0; time.Now().Before(end); seq++ {
				c.say(clients, r, seq)
				gap := *minGap + time.Duration(r.Int63n(int64(*maxGap-*minGap)))
				time.Sleep(gap)
			}
		}(c)
	}
	cw.Wait()
	time.Sleep(6 * time.Second) // let the last messages land
	for _, c := range clients {
		c.mu.Lock()
		if c.in != nil && !c.dropped {
			fmt.Fprint(c.in, "/quit\r")
		}
		c.mu.Unlock()
	}
	time.Sleep(time.Second)
	report(clients, connect, *duration, end)
}

func (c *client) connect(addr string, cfg *ssh.ClientConfig) {
	start := time.Now()
	conn, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		fmt.Printf("user %d: dial failed: %v\n", c.id, err)
		return
	}
	sess, err := conn.NewSession()
	if err != nil {
		return
	}
	if err := sess.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		return
	}
	out, _ := sess.StdoutPipe()
	in, _ := sess.StdinPipe()
	if err := sess.Shell(); err != nil {
		return
	}
	c.sess, c.in = sess, in
	firstScreen := make(chan struct{})
	go func() {
		var tail string
		buf := make([]byte, 16384)
		announced := false
		for {
			n, err := out.Read(buf)
			if n > 0 {
				now := time.Now()
				chunk := tail + string(buf[:n])
				if !announced && strings.Contains(chunk, "#main") {
					announced = true
					c.mu.Lock()
					c.connectMs = float64(now.Sub(start).Milliseconds())
					c.readyAt = now
					c.mu.Unlock()
					close(firstScreen)
				}
				c.scan(chunk, now)
				if len(chunk) > 200 {
					tail = chunk[len(chunk)-200:]
				} else {
					tail = chunk
				}
			}
			if err != nil {
				c.mu.Lock()
				c.dropped = true
				c.mu.Unlock()
				return
			}
		}
	}()
	select {
	case <-firstScreen:
	case <-time.After(20 * time.Second):
		fmt.Printf("user %d: no screen within 20s\n", c.id)
	}
}

// scan records first sightings of message tokens, and server refusals.
func (c *client) scan(chunk string, now time.Time) {
	for _, m := range tokenRe.FindAllStringSubmatch(chunk, -1) {
		tok := m[0]
		c.mu.Lock()
		if c.seen[tok] {
			c.mu.Unlock()
			continue
		}
		c.seen[tok] = true
		c.mu.Unlock()
		sender, _ := strconv.Atoi(m[1])
		res.mu.Lock()
		t0, ok := res.sentAt[tok]
		if ok {
			if sender == c.id {
				res.selfLags = append(res.selfLags, sample{t0, now.Sub(t0)})
			} else {
				res.lags = append(res.lags, sample{t0, now.Sub(t0)})
				res.got[tok]++
			}
		}
		res.mu.Unlock()
	}
	for _, w := range []string{"too fast", "too many messages", "disconnected for flooding", "Too many connections"} {
		if strings.Contains(chunk, w) {
			c.mu.Lock()
			c.sawErr[w] = true
			c.mu.Unlock()
		}
	}
}

func (c *client) say(all []*client, r *rand.Rand, seq int) {
	tok := fmt.Sprintf("(m%d.%d)", c.id, seq)
	phrase := phrases[r.Intn(len(phrases))]
	line := phrase + " " + tok
	if r.Intn(20) == 0 {
		line = "/me " + strings.ToLower(phrase) + " " + tok
	}
	now := time.Now()
	exp := 0
	for _, o := range all {
		o.mu.Lock()
		if o != c && !o.readyAt.IsZero() && !o.dropped {
			exp++
		}
		o.mu.Unlock()
	}
	res.mu.Lock()
	res.sentAt[tok] = now
	res.sentList = append(res.sentList, tok)
	res.expected[tok] = exp
	res.mu.Unlock()
	c.mu.Lock()
	if !c.dropped {
		fmt.Fprint(c.in, line+"\r")
	}
	c.mu.Unlock()
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	i := int(float64(len(d)-1) * p)
	return d[i]
}

func stats(ss []sample) (n int, p50, p90, p99, max, mean time.Duration) {
	d := make([]time.Duration, len(ss))
	var sum time.Duration
	for i, s := range ss {
		d[i] = s.lag
		sum += s.lag
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	if len(d) == 0 {
		return
	}
	return len(d), pct(d, .5), pct(d, .9), pct(d, .99), d[len(d)-1], sum / time.Duration(len(d))
}

func ms(d time.Duration) string { return fmt.Sprintf("%7.1f ms", float64(d.Microseconds())/1000) }

func report(clients []*client, connect []float64, dur time.Duration, end time.Time) {
	sort.Float64s(connect)
	fmt.Println("\n================ RESULTS ================")
	fmt.Printf("connect time to first screen: p50 %.0f ms, p95 %.0f ms, max %.0f ms (n=%d)\n",
		connect[len(connect)/2], connect[int(float64(len(connect)-1)*.95)], connect[len(connect)-1], len(connect))

	res.mu.Lock()
	defer res.mu.Unlock()
	n, p50, p90, p99, max, mean := stats(res.lags)
	fmt.Printf("\nmessages sent: %d   deliveries observed: %d\n", len(res.sentList), n)
	fmt.Printf("DELIVERY LAG (typed -> visible to another user), n=%d\n", n)
	fmt.Printf("   mean %s   p50 %s   p90 %s   p99 %s   max %s\n", ms(mean), ms(p50), ms(p90), ms(p99), ms(max))
	sn, s50, _, s99, smax, _ := stats(res.selfLags)
	fmt.Printf("OWN-MESSAGE ECHO (typed -> visible to sender), n=%d\n   p50 %s   p99 %s   max %s\n", sn, ms(s50), ms(s99), ms(smax))

	// lag per 30-second window, by send time
	start := end.Add(-dur)
	fmt.Println("\nlag by 30 s window (p50 / p99 / max, deliveries):")
	for w := time.Duration(0); w < dur+30*time.Second; w += 30 * time.Second {
		var win []sample
		for _, s := range res.lags {
			if s.at.After(start.Add(w)) && !s.at.After(start.Add(w+30*time.Second)) {
				win = append(win, s)
			}
		}
		if len(win) == 0 {
			continue
		}
		c, a, _, b, m, _ := stats(win)
		fmt.Printf("  +%3.0fs: %s / %s / %s  (%d)\n", w.Seconds(), ms(a), ms(b), ms(m), c)
	}

	// loss: messages older than 10 s that some expected receiver never showed
	var exp, got, lostMsgs int
	cut := time.Now().Add(-10 * time.Second)
	for tok, t0 := range res.sentAt {
		if t0.After(cut) {
			continue
		}
		e, g := res.expected[tok], res.got[tok]
		exp += e
		got += g
		if g < e {
			lostMsgs++
		}
	}
	fmt.Printf("\nDELIVERY: %d of %d expected deliveries seen (%.2f%%); %d messages missed by at least one user\n",
		got, exp, 100*float64(got)/float64(max64(exp, 1)), lostMsgs)

	dropped, flagged := 0, map[string]int{}
	for _, c := range clients {
		c.mu.Lock()
		if c.dropped && !c.readyAt.IsZero() {
			dropped++
		}
		for k := range c.sawErr {
			flagged[k]++
		}
		c.mu.Unlock()
	}
	fmt.Printf("users disconnected before the end: %d\n", dropped)
	for k, v := range flagged {
		fmt.Printf("users who saw %q: %d\n", k, v)
	}
}

func max64(a, b int) int {
	if a > b {
		return a
	}
	return b
}
