// Package ratelimit provides simple abuse-mitigation primitives for a
// zero-authentication SSH server: a per-IP concurrent connection cap.
package ratelimit

import (
	"net"
	"sync"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
)

type ConnLimiter struct {
	max int

	mu     sync.Mutex
	counts map[string]int
}

func NewConnLimiter(max int) *ConnLimiter {
	return &ConnLimiter{max: max, counts: make(map[string]int)}
}

func (c *ConnLimiter) acquire(ip string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts[ip] >= c.max {
		return false
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
				wish.Fatalln(sess, "Too many concurrent connections from your address.")
				return
			}
			defer c.release(ip)
			next(sess)
		}
	}
}
