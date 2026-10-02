package hub

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrbc42/ssh-chat/internal/store"
)

func newTestHub(t *testing.T) *Hub {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHub(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// next returns the next Outbound from s, skipping nothing, or fails on timeout.
func next(t *testing.T, s *Session) Outbound {
	t.Helper()
	select {
	case o := <-s.Outbox:
		return o
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for outbound to %s", s.Nick())
		return Outbound{}
	}
}

// nextLine skips non-line outbounds and joins until a Line of the wanted kind.
func nextLine(t *testing.T, s *Session, kind LineKind) Line {
	t.Helper()
	for {
		o := next(t, s)
		if o.Line != nil && o.Line.Kind == kind {
			return *o.Line
		}
	}
}

func quiet(s *Session) bool {
	select {
	case o := <-s.Outbox:
		return o.Line == nil || o.Line.Kind != KindChat && o.Line.Kind != KindAction && o.Line.Kind != KindPM
	case <-time.After(300 * time.Millisecond):
		return true
	}
}

func TestMeTimeIgnoreClear(t *testing.T) {
	ctx := context.Background()
	h := newTestHub(t)
	alice := NewSession("fp-a", "1.1.1.1", "alice")
	bob := NewSession("fp-b", "2.2.2.2", "bob")
	h.Main().Join(alice)
	h.Main().Join(bob)

	// /me reaches everyone as an action line.
	HandleInput(ctx, h, alice, "/me waves")
	if l := nextLine(t, bob, KindAction); l.Sender != "alice" || l.Body != "waves" {
		t.Fatalf("action = %+v", l)
	}

	// /time reports online time to the caller.
	HandleInput(ctx, h, alice, "/time")
	if l := nextLine(t, alice, KindInfo); !strings.Contains(l.Body, "online for") {
		t.Fatalf("time = %q", l.Body)
	}

	// bob ignores alice: chat, actions and PMs vanish; others still arrive.
	HandleInput(ctx, h, bob, "/ignore Alice")
	drain(bob)
	HandleInput(ctx, h, alice, "hello")
	HandleInput(ctx, h, alice, "/me jumps")
	HandleInput(ctx, h, alice, "/msg bob psst")
	for i := 0; i < 3; i++ {
		if !quiet(bob) {
			t.Fatal("bob received a line from an ignored user")
		}
	}
	// alice still sees her own PM echo, so she isn't tipped off.
	if l := nextLine(t, alice, KindPM); l.Dir != "to" {
		t.Fatalf("pm echo = %+v", l)
	}

	// Un-ignore restores delivery.
	HandleInput(ctx, h, bob, "/unignore alice")
	drain(bob)
	HandleInput(ctx, h, alice, "back")
	if l := nextLine(t, bob, KindChat); l.Body != "back" {
		t.Fatalf("chat = %+v", l)
	}

	// /clear sends a Clear outbound.
	HandleInput(ctx, h, bob, "/clear")
	for {
		if next(t, bob).Clear {
			break
		}
	}

	// Can't ignore yourself.
	HandleInput(ctx, h, bob, "/ignore bob")
	if l := nextLine(t, bob, KindInfo); !strings.Contains(l.Body, "yourself") {
		t.Fatalf("self-ignore = %q", l.Body)
	}
}

func drain(s *Session) {
	time.Sleep(200 * time.Millisecond)
	for {
		select {
		case <-s.Outbox:
		default:
			return
		}
	}
}
