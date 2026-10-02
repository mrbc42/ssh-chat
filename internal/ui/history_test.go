package ui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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
