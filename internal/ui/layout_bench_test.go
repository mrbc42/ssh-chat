package ui

import (
	"context"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/mrbc42/ssh-chat/internal/hub"
	"github.com/mrbc42/ssh-chat/internal/store"
)

func benchModel(b *testing.B) Model {
	b.Helper()
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	st, err := store.Open(filepath.Join(b.TempDir(), "b.db"))
	if err != nil {
		b.Fatal(err)
	}
	h, err := hub.NewHub(st, nil)
	if err != nil {
		b.Fatal(err)
	}
	m := Model{h: h, sty: newStyles(r), textinput: textinput.New(), width: 120, height: 40, sess: hub.NewSession("SHA256:b", "", "bench")}
	at := time.Date(2026, 10, 3, 22, 45, 0, 0, time.UTC)
	for i := 0; i < 50; i++ {
		m.lines = append(m.lines, hub.Line{Time: at, Kind: hub.KindChat, Sender: "user" + strconv.Itoa(i%9), Body: "morning all, anyone around for a chat today? " + strconv.Itoa(i)})
	}
	return m
}

// One channel hop: re-lay-out a 50-line history.
func BenchmarkLayout50Cached(b *testing.B) {
	m := benchModel(b)
	m.layout() // warm the shared render cache
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.layout()
	}
}

func BenchmarkAppendLineCached(b *testing.B) {
	m := benchModel(b)
	m.layout()
	l := m.lines[7]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.appendLine(l)
	}
}

// /history against a real hub and database: older messages are prepended,
// oldest first, the view lands on the newest of them, and the start of the
// channel is announced exactly once.
func TestHistoryCommandPagesBackThroughTheChannel(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := hub.NewHub(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := st.GetOrCreateMainChannel(ctx)
	for i := 1; i <= 260; i++ {
		who := "bob"
		if i%10 == 0 {
			who = "noisy"
		}
		if _, err := st.AppendMessage(ctx, store.Message{ChannelID: ch.ID, SenderFP: "fp", SenderName: who, Body: "line " + strconv.Itoa(i), Kind: "msg"}); err != nil {
			t.Fatal(err)
		}
	}
	sess := hub.NewSession("SHA256:reader", "", "reader")
	sess.Ignore("noisy")
	h.Main().Join(sess)
	time.Sleep(100 * time.Millisecond)

	r := lipgloss.NewRenderer(io.Discard)
	m := Model{ctx: ctx, h: h, sess: sess, sty: newStyles(r), textinput: textinput.New(), width: 80, height: 30, mouse: true}
	first, exhausted, err := h.History(ctx, sess, 0, 50) // what a join replays
	if err != nil || exhausted || len(first) != 45 {     // 50 messages minus the 5 from the ignored user
		t.Fatalf("setup: %d lines exhausted=%v err=%v", len(first), exhausted, err)
	}
	m.lines = first
	m.layout()
	if m.oldestID() == 0 {
		t.Fatal("replayed lines must carry their database ids")
	}

	m.windowCommand("/history 100")
	if len(m.lines) <= 50 || m.scroll == 0 {
		t.Fatalf("no history loaded: %d lines, scroll %d", len(m.lines), m.scroll)
	}
	for i := 1; i < len(m.lines); i++ {
		if m.lines[i].ID != 0 && m.lines[i-1].ID != 0 && m.lines[i].ID <= m.lines[i-1].ID {
			t.Fatalf("history not oldest-first at %d: %d then %d", i, m.lines[i-1].ID, m.lines[i].ID)
		}
		if m.lines[i].Sender == "noisy" {
			t.Fatal("an ignored user's lines must not come back through /history")
		}
	}
	// The bottom of the screen shows the newest of the loaded-older lines,
	// i.e. the one just before what was on screen before.
	vis := m.visibleRows()
	last := ansiStrip(vis[len(vis)-1])
	want := "line " + strconv.Itoa(int(m.oldestIDOfOriginal(first)-1))
	if !strings.Contains(last, want) && !strings.Contains(ansiStrip(strings.Join(vis[len(vis)-2:], " ")), want) {
		t.Fatalf("view should end at %q, bottom rows: %q", want, vis[len(vis)-2:])
	}

	m.windowCommand("/history 500") // fetches the rest and reaches the start
	if !m.historyDone || !strings.Contains(m.lines[0].Body, "start of this channel's history") {
		t.Fatalf("start of history not marked: done=%v first=%q", m.historyDone, m.lines[0].Body)
	}
	before := len(m.lines)
	m.windowCommand("/history")
	if !strings.Contains(m.lines[len(m.lines)-1].Body, "already at the start") || len(m.lines) != before+1 {
		t.Fatal("asking again at the start should say so, once")
	}

	m.windowCommand("/history abc")
	m.windowCommand("/history 9999")
	if !strings.Contains(m.lines[len(m.lines)-1].Body, "Usage: /history") {
		t.Fatalf("bad argument not rejected: %q", m.lines[len(m.lines)-1].Body)
	}
}

// oldestIDOfOriginal is test glue: the smallest database id among lines.
func (m *Model) oldestIDOfOriginal(lines []hub.Line) int64 {
	var oldest int64
	for _, l := range lines {
		if l.ID > 0 && (oldest == 0 || l.ID < oldest) {
			oldest = l.ID
		}
	}
	return oldest
}
