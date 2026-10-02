package ui

import (
	"io"
	"testing"

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
