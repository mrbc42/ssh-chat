package sshserver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
)

// The bug: a closed window-change channel made the forwarding loop spin,
// firing resize messages without end.
func TestClosedWindowChannelEndsTheLoopInsteadOfSpinning(t *testing.T) {
	changes := make(chan ssh.Window)
	close(changes)
	var sent, quits atomic.Int64
	done := make(chan struct{})
	go func() {
		forwardWindowChanges(context.Background(), changes, func(tea.Msg) { sent.Add(1) }, func() { quits.Add(1) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("loop did not stop on a closed channel (spun %d resize messages)", sent.Load())
	}
	if sent.Load() != 0 || quits.Load() != 1 {
		t.Fatalf("closed channel: sent=%d quits=%d, want 0 and 1", sent.Load(), quits.Load())
	}
}

func TestResizesAreForwardedThenContextEndsTheLoop(t *testing.T) {
	changes := make(chan ssh.Window, 2)
	ctx, cancel := context.WithCancel(context.Background())
	var got []tea.WindowSizeMsg
	var quits atomic.Int64
	done := make(chan struct{})
	changes <- ssh.Window{Width: 100, Height: 30}
	changes <- ssh.Window{Width: 120, Height: 40}
	go func() {
		forwardWindowChanges(ctx, changes, func(m tea.Msg) { got = append(got, m.(tea.WindowSizeMsg)) }, func() { quits.Add(1) })
		close(done)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(changes) > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if len(got) != 2 || got[0].Width != 100 || got[1].Height != 40 || quits.Load() != 1 {
		t.Fatalf("resizes=%v quits=%d", got, quits.Load())
	}
}
