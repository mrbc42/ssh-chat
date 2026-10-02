// Package ratelimit provides simple abuse-mitigation primitives for a
// zero-authentication SSH server: a per-IP cap on concurrent connections and on
// how often new connections may be opened.
package ratelimit

import (
	"net"
	"sync"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
)

type ConnLimiter struct {
	max       int
	perMinute int // 0 = no limit on connection rate

	mu     sync.Mutex
	counts map[string]int
	opened map[string][]time.Time // recent accepted connection times per IP
	now    func() time.Time
}

func NewConnLimiter(max int) *ConnLimiter {
	return NewConnLimiterRate(max, 0)
}

// NewConnLimiterRate also refuses an address that has opened more than
// perMinute connections in the last minute (reconnect flooding), even if each
// one closes again straight away.
func NewConnLimiterRate(max, perMinute int) *ConnLimiter {
	return &ConnLimiter{max: max, perMinute: perMinute, counts: make(map[string]int),
		opened: make(map[string][]time.Time), now: time.Now}
}

func (c *ConnLimiter) acquire(ip string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts[ip] >= c.max {
		return false
	}
	if c.perMinute > 0 {
		now := c.now()
		cut := now.Add(-time.Minute)
		recent := c.opened[ip][:0]
		for _, t := range c.opened[ip] {
			if t.After(cut) {
				recent = append(recent, t)
			}
		}
		if len(recent) >= c.perMinute {
			c.opened[ip] = recent
			return false
		}
		c.opened[ip] = append(recent, now)
	}
	c.counts[ip]++
	return true
}

func (c *ConnLimiter) release(ip string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[ip]--
	if c.counts[ip] <= 0 {
		delete(c.counts, ip)
	}
}

func ipOf(sess ssh.Session) string {
	addr := sess.RemoteAddr()
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

// Middleware rejects a new connection once the source IP already has `max`
// concurrent sessions open, so a single misbehaving client can't exhaust
// server resources on this zero-auth, open-to-anyone server.
func Middleware(c *ConnLimiter) wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			ip := ipOf(sess)
			if !c.acquire(ip) {
				wish.Fatalln(sess, "Too many connections from your address. Slow down and try again shortly.")
				return
			}
			defer c.release(ip)
			next(sess)
		}
	}
}
