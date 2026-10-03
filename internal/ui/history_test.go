package ui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mrbc42/ssh-chat/internal/hub"
	"github.com/muesli/termenv"
)

func TestHistoryArrows(t *testing.T) {
	ti := textinput.New()
	ti.Focus()
	var m tea.Model = Model{textinput: ti}
	mm := m.(Model)
	mm.addHistory("one")
	mm.addHistory("two")
	m = mm
	press := func(k tea.KeyType) string {
		m, _ = m.Update(tea.KeyMsg{Type: k})
		return m.(Model).textinput.Value()
	}
	if got := press(tea.KeyUp); got != "two" {
		t.Fatalf("up1 = %q", got)
	}
	if got := press(tea.KeyUp); got != "one" {
		t.Fatalf("up2 = %q", got)
	}
	if got := press(tea.KeyDown); got != "two" {
		t.Fatalf("down1 = %q", got)
	}
	if got := press(tea.KeyDown); got != "" {
		t.Fatalf("down2 = %q", got)
	}
}

func TestPromptShowsNickInChatColour(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	sess := hub.NewSession("SHA256:x", "", "cosmic-badger")
	ti := textinput.New()
	m := Model{sess: sess, sty: newStyles(r), textinput: ti, width: 80}

	m.syncPrompt()
	if m.textinput.Prompt != "cosmic-badger> " {
		t.Fatalf("prompt = %q", m.textinput.Prompt)
	}
	if got, want := m.textinput.PromptStyle.Render("x"), m.sty.NickStyle("cosmic-badger").Render("x"); got != want || got == "x" {
		t.Fatalf("prompt colour %q does not match chat nick colour %q", got, want)
	}
	if m.textinput.Width != 80-len("cosmic-badger> ")-1 {
		t.Fatalf("input width not reduced by the prompt: %d", m.textinput.Width)
	}

	// A /nick is reflected on the next sync, with the new name's colour.
	sess.SetNick("bob")
	m.syncPrompt()
	if m.textinput.Prompt != "bob> " || m.textinput.PromptStyle.Render("x") != m.sty.NickStyle("bob").Render("x") {
		t.Fatalf("prompt did not follow /nick: %q", m.textinput.Prompt)
	}
}

func TestLongMessagesWrapWithHangingIndent(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard) // plain text: no ANSI to strip
	sty := newStyles(r)
	at := time.Date(2026, 10, 3, 22, 45, 0, 0, time.UTC)
	body := "Another caller: lucky-wombat. The modem pool was getting lonely."
	rows := renderRows(hub.Line{Time: at, Kind: hub.KindChat, Sender: "SysOp-Gus", Body: body}, sty, 60)

	prefix := "[22:45] SysOp-Gus: "
	if len(rows) < 2 || !strings.HasPrefix(rows[0], prefix) {
		t.Fatalf("expected a wrapped message starting with %q, got %q", prefix, rows)
	}
	var words []string
	for i, row := range rows {
		if w := lipgloss.Width(row); w > 60 {
			t.Errorf("row %d is %d wide, want <= 60: %q", i, w, row)
		}
		if i > 0 {
			if !strings.HasPrefix(row, strings.Repeat(" ", len(prefix))) || strings.TrimSpace(row) == "" {
				t.Errorf("continuation row %d is not indented under the message text: %q", i, row)
			}
			// No second timestamp or name: it is one message.
			if strings.Contains(row, "[22:45]") || strings.Contains(row, "SysOp-Gus") {
				t.Errorf("continuation repeats the prefix: %q", row)
			}
		}
		text := strings.TrimPrefix(row, prefix)
		words = append(words, strings.Fields(text)...)
	}
	if got := strings.Join(words, " "); got != body {
		t.Errorf("wrapping changed the text:\n got %q\nwant %q", got, body)
	}

	// A short message stays on one row; a very long word is hard-split, not lost.
	if rows := renderRows(hub.Line{Time: at, Kind: hub.KindChat, Sender: "bob", Body: "hi"}, sty, 60); len(rows) != 1 {
		t.Errorf("short message wrapped: %q", rows)
	}
	long := strings.Repeat("x", 100)
	rows = renderRows(hub.Line{Time: at, Kind: hub.KindChat, Sender: "bob", Body: long}, sty, 40)
	if strings.Count(strings.Join(rows, ""), "x") != 100 {
		t.Errorf("hard-split dropped characters: %q", rows)
	}
	// Narrow terminal / long nick: fall back to flush-left wrapping, never panic.
	_ = renderRows(hub.Line{Time: at, Kind: hub.KindChat, Sender: strings.Repeat("n", 24), Body: body}, sty, 30)
	_ = renderRows(hub.Line{Time: at, Kind: hub.KindSystem, Body: body}, sty, 0)
}
