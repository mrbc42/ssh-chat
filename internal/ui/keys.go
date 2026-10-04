package ui

import "github.com/mrbc42/ssh-chat/internal/hub"

// keyHelp is what /keys shows. Keep it in step with Update's key handling
// (a test presses the documented scrolling keys) and with the bubbles
// textinput key map for the editing keys.
var keyHelp = []struct {
	heading string
	rows    [][2]string
}{
	{"Chat", [][2]string{
		{"Enter", "Send the line (or run the /command)"},
		{"Tab", "Complete a /command name; press again to cycle the matches"},
		{"Up / Down", "Recall your previous / next sent lines"},
		{"Ctrl+C", "Disconnect"},
	}},
	{"Scrolling the chat", [][2]string{
		{"PgUp / PgDn", "Scroll a screenful"},
		{"Alt+Up / Alt+Down", "Scroll 3 lines"},
		{"Mouse wheel", "Scroll 3 lines (/mouse turns this off so you can select text)"},
		{"Ctrl+Home / Ctrl+End", "Jump to the oldest / newest message"},
		{"Home", "Jump to the oldest message (only when the input line is empty)"},
		{"End", "Jump to the newest message (only while scrolled up)"},
		{"/history [n]", "Load older messages for this channel (default 100, max 500)"},
	}},
	{"Editing the input line", [][2]string{
		{"Left / Right", "Move the cursor"},
		{"Home / End  or  Ctrl+A / Ctrl+E", "Start / end of the line"},
		{"Alt+Left / Alt+Right", "Move by a word"},
		{"Ctrl+W", "Delete the word before the cursor"},
		{"Ctrl+K / Ctrl+U", "Delete to the end / start of the line"},
		{"Delete / Backspace", "Delete the character after / before the cursor"},
	}},
}

// keyHelpLines renders keyHelp as info lines for the chat pane.
func keyHelpLines() []hub.Line {
	var out []hub.Line
	add := func(text string) { out = append(out, hub.Line{Kind: hub.KindInfo, Body: text}) }
	add("Keyboard and mouse shortcuts:")
	for _, g := range keyHelp {
		add(g.heading + ":")
		for _, r := range g.rows {
			add("  " + padRight(r[0], 34) + " " + r[1])
		}
	}
	return out
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}
