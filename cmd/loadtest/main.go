// Command loadtest connects many anonymous (keyless) users to an ssh-chat
// server and has them chat like a normal conversation, optionally spread over
// many channels with users hopping between channels, leaving and re-entering
// #main, and logging off and back on. It reports how long messages take to
// reach the other people in the same channel.
//
// Every message carries a token "(mSENDER.SEQ)". The sender records when it
// typed the message; every other client records when that token first appears
// on its screen. All clients share one process clock, so no clock sync is
// needed. Tokens that show up as scrollback replay after a channel switch are
// ignored (they were sent before the receiver joined).
package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

type presence struct {
	at   time.Time
	room string // "" = offline
}

type user struct {
	id   int
	mu   sync.Mutex
	line []presence // intended room over time
	cur  *conn
	room string // intended room right now ("" when offline)
}

func (u *user) set(room string, at time.Time) {
	u.mu.Lock()
	u.room = room
	u.line = append(u.line, presence{at, room})
	u.mu.Unlock()
}

// stableIn reports whether u was in room for all of [from, to], with no moves.
func (u *user) stableIn(room string, from, to time.Time) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	state := ""
	for _, p := range u.line {
		if !p.at.After(from) {
			state = p.room
			continue
		}
		if !p.at.After(to) {
			return false // moved during the window
		}
	}
	return state == room
}

func (u *user) roomNow() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.room
}

type conn struct {
	u        *user
	client   *ssh.Client
	sess     *ssh.Session
	in       io.WriteCloser
	mu       sync.Mutex
	seen     map[string]bool
	readyAt  time.Time
	joinedAt time.Time // when the move into the current room was requested
	quitting bool
	dropped  bool
	sawErr   map[string]bool
}

type sent struct {
	at     time.Time
	room   string
	sender int
}

type lagSample struct {
	at   time.Time
	lag  time.Duration
	main bool
}

type results struct {
	mu      sync.Mutex
	sent    map[string]sent
	seenBy  map[string]map[int]bool
	lags    []lagSample
	self    []lagSample
	connect []float64
	stats   map[string]*int64
}

var res = &results{sent: map[string]sent{}, seenBy: map[string]map[int]bool{}, stats: map[string]*int64{}}

func count(name string) {
	res.mu.Lock()
	p, ok := res.stats[name]
	if !ok {
		p = new(int64)
		res.stats[name] = p
	}
	res.mu.Unlock()
	atomic.AddInt64(p, 1)
}

var (
	nChannels       int
	leakConnections bool
	endAt           time.Time
	seqNo           int64
)

func roomName(i int) string { return fmt.Sprintf("room%02d", i+1) }

func main() {
	addr := flag.String("addr", "127.0.0.1:2224", "server host:port")
	users := flag.Int("users", 50, "concurrent anonymous users")
	channels := flag.Int("channels", 0, "spread users over this many channels besides #main (0 = everyone stays in #main)")
	churn := flag.Bool("churn", false, "users keep hopping channels, leaving/entering #main and logging off/on")
	duration := flag.Duration("duration", 3*time.Minute, "how long the conversation runs once everyone is connected")
	ramp := flag.Duration("ramp", 25*time.Second, "spread the first connections over this long")
	minGap := flag.Duration("min", 4*time.Second, "shortest pause between one user's messages")
	maxGap := flag.Duration("max", 12*time.Second, "longest pause between one user's messages")
	flag.BoolVar(&leakConnections, "leak", false, "close each session but leave its SSH connection open (a rude client)")
	seed := flag.Int64("seed", time.Now().UnixNano(), "random seed")
	flag.Parse()
	nChannels = *channels

	cfg := &ssh.ClientConfig{
		User:            "loadtest",
		Auth:            []ssh.AuthMethod{ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { return nil, nil })},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}

	all := make([]*user, *users)
	for i := range all {
		all[i] = &user{id: i}
	}

	// Pre-create the channels so users don't race to create them.
	if nChannels > 0 {
		fmt.Printf("creating %d channels ...\n", nChannels)
		var cwg sync.WaitGroup
		for i := 0; i < nChannels; i++ {
			cwg.Add(1)
			go func(i int) {
				defer cwg.Done()
				c := dial(*addr, cfg, all[i%len(all)])
				if c == nil {
					return
				}
				c.send("/create " + roomName(i) + "\r")
				time.Sleep(1500 * time.Millisecond)
				c.quit()
			}(i)
		}
		cwg.Wait()
		time.Sleep(2 * time.Second)
		for _, u := range all { // creators' presence history is irrelevant
			u.line = nil
			u.room = ""
		}
	}

	startAt := time.Now()
	endAt = startAt.Add(*ramp + *duration)
	fmt.Printf("starting %d users (ramp %s, then %s of conversation; channels=%d churn=%v) ...\n", *users, *ramp, *duration, nChannels, *churn)

	var wg sync.WaitGroup
	for _, u := range all {
		wg.Add(1)
		go func(u *user) {
			defer wg.Done()
			time.Sleep(time.Duration(float64(*ramp) * float64(u.id) / float64(*users)))
			u.run(*addr, cfg, *minGap, *maxGap, *churn, rand.New(rand.NewSource(*seed+int64(u.id))))
		}(u)
	}

	// Progress + membership sampling.
	stop := make(chan struct{})
	var peak int
	var occupancy []map[string]int
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				m := map[string]int{}
				online := 0
				for _, u := range all {
					if r := u.roomNow(); r != "" {
						m[r]++
						online++
					}
				}
				if online > peak {
					peak = online
				}
				occupancy = append(occupancy, m)
				res.mu.Lock()
				fmt.Printf("[+%3.0fs] online %d  in #main %d  messages sent %d  deliveries %d\n",
					time.Since(startAt).Seconds(), online, m["main"], len(res.sent), len(res.lags))
				res.mu.Unlock()
			}
		}
	}()

	wg.Wait()
	close(stop)
	time.Sleep(6 * time.Second)
	report(all, *ramp, startAt, endAt, peak, occupancy)
}

func (u *user) run(addr string, cfg *ssh.ClientConfig, minGap, maxGap time.Duration, churn bool, r *rand.Rand) {
	for time.Now().Before(endAt) {
		c := dial(addr, cfg, u)
		if c == nil {
			time.Sleep(2 * time.Second)
			continue
		}
		count("logins")
		logoff := c.converse(r, minGap, maxGap, churn)
		if logoff {
			count("logoffs")
			c.quit()
			time.Sleep(3*time.Second + time.Duration(r.Int63n(int64(12*time.Second))))
			continue
		}
		c.quit()
		return
	}
}

// converse chats and (optionally) moves around until the test ends or the
// user decides to log off, in which case it returns true.
func (c *conn) converse(r *rand.Rand, minGap, maxGap time.Duration, churn bool) (logoff bool) {
	u := c.u
	nextSay := time.Now().Add(time.Duration(r.Int63n(int64(maxGap))))
	var nextMove time.Time
	if nChannels > 0 {
		nextMove = time.Now().Add(time.Second + time.Duration(r.Int63n(int64(5*time.Second)))) // first hop out of #main
	}
	for time.Now().Before(endAt) {
		c.mu.Lock()
		gone := c.dropped
		c.mu.Unlock()
		if gone {
			count("unexpected_drops")
			return true // reconnect
		}
		now := time.Now()
		switch {
		case !nextMove.IsZero() && now.After(nextMove):
			cur := u.roomNow()
			act := r.Intn(100)
			switch {
			case cur == "main" || !churn && cur == "": // from main: always out to a channel
				c.join(roomName(r.Intn(nChannels)))
				count("moves_out_of_main")
			case !churn:
				// no churn: stay put after the first placement
			case act < 55:
				c.join(roomName(r.Intn(nChannels)))
				count("moves_between_channels")
			case act < 75:
				c.leaveToMain()
				count("moves_into_main")
			case act < 90:
				return true
			}
			if churn {
				nextMove = time.Now().Add(20*time.Second + time.Duration(r.Int63n(int64(40*time.Second))))
			} else {
				nextMove = time.Time{}
			}
		case now.After(nextSay):
			c.say(r)
			nextSay = time.Now().Add(minGap + time.Duration(r.Int63n(int64(maxGap-minGap))))
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
	return false
}

func dial(addr string, cfg *ssh.ClientConfig, u *user) *conn {
	start := time.Now()
	nc, err := net.DialTimeout("tcp", addr, 20*time.Second)
	if err != nil {
		count("dial_errors")
		return nil
	}
	_ = nc.SetDeadline(time.Now().Add(30 * time.Second)) // bound the handshake
	sc, chans, reqs, err := ssh.NewClientConn(nc, addr, cfg)
	if err != nil {
		nc.Close()
		count("handshake_errors")
		return nil
	}
	_ = nc.SetDeadline(time.Time{})
	cl := ssh.NewClient(sc, chans, reqs)
	sess, err := cl.NewSession()
	if err != nil {
		return nil
	}
	if err := sess.RequestPty("xterm-256color", 40, 120, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
		return nil
	}
	out, _ := sess.StdoutPipe()
	in, _ := sess.StdinPipe()
	if err := sess.Shell(); err != nil {
		return nil
	}
	c := &conn{u: u, client: cl, sess: sess, in: in, seen: map[string]bool{}, sawErr: map[string]bool{}}
	ready := make(chan struct{})
	go func() {
		var tail string
		buf := make([]byte, 32768)
		announced := false
		for {
			n, err := out.Read(buf)
			if n > 0 {
				now := time.Now()
				chunk := tail + string(buf[:n])
				if !announced && strings.Contains(chunk, "#main") {
					announced = true
					c.mu.Lock()
					c.readyAt, c.joinedAt = now, now
					c.mu.Unlock()
					u.set("main", now)
					u.mu.Lock()
					u.cur = c
					u.mu.Unlock()
					res.mu.Lock()
					res.connect = append(res.connect, float64(now.Sub(start).Milliseconds()))
					res.mu.Unlock()
					close(ready)
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
				if !c.quitting {
					c.dropped = true
				}
				c.mu.Unlock()
				return
			}
		}
	}()
	select {
	case <-ready:
		return c
	case <-time.After(30 * time.Second):
		count("no_screen")
		sess.Close()
		return nil
	}
}

func (c *conn) send(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dropped {
		fmt.Fprint(c.in, s)
	}
}

func (c *conn) join(room string) {
	now := time.Now()
	c.mu.Lock()
	c.joinedAt = now
	c.mu.Unlock()
	c.u.set(room, now)
	c.send("/join " + room + "\r")
}

func (c *conn) leaveToMain() {
	now := time.Now()
	c.mu.Lock()
	c.joinedAt = now
	c.mu.Unlock()
	c.u.set("main", now)
	c.send("/leave\r")
}

func (c *conn) quit() {
	c.mu.Lock()
	c.quitting = true
	c.mu.Unlock()
	c.u.set("", time.Now())
	if c.in != nil {
		fmt.Fprint(c.in, "/quit\r")
	}
	time.Sleep(300 * time.Millisecond)
	if c.sess != nil {
		c.sess.Close()
	}
	// A real client closes the whole connection. -leak keeps it open to
	// reproduce the rude-client case (session closed, connection kept).
	if c.client != nil && !leakConnections {
		c.client.Close()
	}
}

func (c *conn) say(r *rand.Rand) {
	seq := atomic.AddInt64(&seqNo, 1)
	tok := fmt.Sprintf("(m%d.%d)", c.u.id, seq)
	phrase := phrases[r.Intn(len(phrases))]
	line := phrase + " " + tok
	if r.Intn(20) == 0 {
		line = "/me " + strings.ToLower(phrase) + " " + tok
	}
	now := time.Now()
	room := c.u.roomNow()
	res.mu.Lock()
	res.sent[tok] = sent{now, room, c.u.id}
	res.mu.Unlock()
	c.send(line + "\r")
}

// scan records first sightings of message tokens, and server refusals.
func (c *conn) scan(chunk string, now time.Time) {
	for _, m := range tokenRe.FindAllStringSubmatch(chunk, -1) {
		tok := m[0]
		c.mu.Lock()
		if c.seen[tok] {
			c.mu.Unlock()
			continue
		}
		c.seen[tok] = true
		joined := c.joinedAt
		c.mu.Unlock()
		sender, _ := strconv.Atoi(m[1])
		res.mu.Lock()
		s, ok := res.sent[tok]
		if ok && !s.at.Before(joined) { // older than our last move = scrollback replay
			ls := lagSample{s.at, now.Sub(s.at), s.room == "main"}
			if sender == c.u.id {
				res.self = append(res.self, ls)
			} else {
				res.lags = append(res.lags, ls)
				if res.seenBy[tok] == nil {
					res.seenBy[tok] = map[int]bool{}
				}
				res.seenBy[tok][c.u.id] = true
			}
		}
		res.mu.Unlock()
	}
	for _, w := range []string{"too fast", "too many messages", "disconnected for flooding", "Too many connections", "channel limit", "Failed to"} {
		if strings.Contains(chunk, w) {
			c.mu.Lock()
			c.sawErr[w] = true
			c.mu.Unlock()
		}
	}
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	return d[int(float64(len(d)-1)*p)]
}

func stats(ss []lagSample) (n int, mean, p50, p90, p99, max time.Duration) {
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
	return len(d), sum / time.Duration(len(d)), pct(d, .5), pct(d, .9), pct(d, .99), d[len(d)-1]
}

func ms(d time.Duration) string { return fmt.Sprintf("%8.1f ms", float64(d.Microseconds())/1000) }

func report(all []*user, ramp time.Duration, start, end time.Time, peak int, occ []map[string]int) {
	fmt.Println("\n================ RESULTS ================")
	res.mu.Lock()
	defer res.mu.Unlock()

	c := append([]float64(nil), res.connect...)
	sort.Float64s(c)
	if len(c) > 0 {
		fmt.Printf("logins: %d   connect time to first screen: p50 %.0f ms, p95 %.0f ms, max %.0f ms\n",
			len(c), c[len(c)/2], c[int(float64(len(c)-1)*.95)], c[len(c)-1])
	}
	names := make([]string, 0, len(res.stats))
	for k := range res.stats {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Printf("  %-24s %d\n", k, atomic.LoadInt64(res.stats[k]))
	}
	fmt.Printf("peak users online at once: %d\n", peak)
	if len(occ) > 0 {
		var mainSum, chanSum, chanN int
		for _, m := range occ {
			mainSum += m["main"]
			for k, v := range m {
				if k != "main" {
					chanSum += v
					chanN++
				}
			}
		}
		fmt.Printf("average people: in #main %.1f; per occupied channel %.1f (over %d samples)\n",
			float64(mainSum)/float64(len(occ)), float64(chanSum)/float64(max1(chanN)), len(occ))
	}

	n, mean, p50, p90, p99, mx := stats(res.lags)
	fmt.Printf("\nmessages sent: %d   deliveries observed: %d\n", len(res.sent), n)
	fmt.Printf("DELIVERY LAG (typed -> visible to another user in the same channel), n=%d\n", n)
	fmt.Printf("   mean %s   p50 %s   p90 %s   p99 %s   max %s\n", ms(mean), ms(p50), ms(p90), ms(p99), ms(mx))
	var mainL, chanL []lagSample
	for _, s := range res.lags {
		if s.main {
			mainL = append(mainL, s)
		} else {
			chanL = append(chanL, s)
		}
	}
	if len(mainL) > 0 {
		n, _, a, b, cc, d := stats(mainL)
		fmt.Printf("   #main only (n=%d): p50 %s  p90 %s  p99 %s  max %s\n", n, ms(a), ms(b), ms(cc), ms(d))
	}
	if len(chanL) > 0 {
		n, _, a, b, cc, d := stats(chanL)
		fmt.Printf("   other channels (n=%d): p50 %s  p90 %s  p99 %s  max %s\n", n, ms(a), ms(b), ms(cc), ms(d))
	}
	sn, _, s50, _, s99, smax := stats(res.self)
	fmt.Printf("OWN-MESSAGE ECHO (typed -> visible to sender), n=%d\n   p50 %s   p99 %s   max %s\n", sn, ms(s50), ms(s99), ms(smax))

	begin := start.Add(ramp)
	fmt.Println("\nlag by 30 s window (p50 / p99 / max, deliveries):")
	for w := -ramp; w < end.Sub(begin)+30*time.Second; w += 30 * time.Second {
		var win []lagSample
		for _, s := range res.lags {
			if s.at.After(begin.Add(w)) && !s.at.After(begin.Add(w+30*time.Second)) {
				win = append(win, s)
			}
		}
		if len(win) == 0 {
			continue
		}
		cnt, _, a, _, b, m := stats(win)
		fmt.Printf("  %+4.0fs: %s / %s / %s  (%d)\n", w.Seconds(), ms(a), ms(b), ms(m), cnt)
	}

	// Loss: only count messages where sender and receivers all stayed put in
	// the same channel from 5 s before to 10 s after the send.
	var exp, got, lostMsgs, judged int
	cutoff := time.Now().Add(-12 * time.Second)
	for tok, s := range res.sent {
		if s.room == "" || s.at.After(cutoff) {
			continue
		}
		from, to := s.at.Add(-5*time.Second), s.at.Add(10*time.Second)
		if !all[s.sender].stableIn(s.room, from, to) {
			continue
		}
		judged++
		missed := false
		for _, u := range all {
			if u.id == s.sender || !u.stableIn(s.room, from, to) {
				continue
			}
			exp++
			if res.seenBy[tok][u.id] {
				got++
			} else {
				missed = true
			}
		}
		if missed {
			lostMsgs++
		}
	}
	fmt.Printf("\nDELIVERY (messages where everyone stayed put in one channel): %d of %d expected deliveries seen (%.2f%%); %d of %d messages missed by someone\n",
		got, exp, 100*float64(got)/float64(max1(exp)), lostMsgs, judged)

	flagged := map[string]int{}
	for _, u := range all {
		u.mu.Lock()
		if u.cur != nil {
			u.cur.mu.Lock()
			for k := range u.cur.sawErr {
				flagged[k]++
			}
			u.cur.mu.Unlock()
		}
		u.mu.Unlock()
	}
	for k, v := range flagged {
		fmt.Printf("users who saw %q on their last connection: %d\n", k, v)
	}
}

func max1(a int) int {
	if a < 1 {
		return 1
	}
	return a
}

var _ = os.Exit
