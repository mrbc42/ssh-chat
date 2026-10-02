package ratelimit

import (
	"testing"
	"time"
)

func TestConcurrentCap(t *testing.T) {
	c := NewConnLimiter(2)
	if !c.acquire("1.1.1.1") || !c.acquire("1.1.1.1") || c.acquire("1.1.1.1") {
		t.Fatal("concurrent cap not enforced")
	}
	c.release("1.1.1.1")
	if !c.acquire("1.1.1.1") {
		t.Fatal("slot not freed")
	}
	if !c.acquire("2.2.2.2") {
		t.Fatal("other addresses must be unaffected")
	}
}

func TestConnectionRateCap(t *testing.T) {
	c := NewConnLimiterRate(100, 5)
	now := time.Now()
	c.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if !c.acquire("1.1.1.1") {
			t.Fatalf("connection %d refused too early", i)
		}
		c.release("1.1.1.1") // reconnect flooding: each one closes straight away
	}
	if c.acquire("1.1.1.1") {
		t.Fatal("6th connection within a minute must be refused")
	}
	if !c.acquire("9.9.9.9") {
		t.Fatal("other address affected")
	}
	now = now.Add(61 * time.Second)
	if !c.acquire("1.1.1.1") {
		t.Fatal("limit should lapse after a minute")
	}
}
