package ui

import (
	"io"
	"path/filepath"
	"strconv"
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
