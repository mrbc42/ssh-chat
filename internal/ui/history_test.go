package ui

import (
	"io"
	"strconv"
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

func TestFastColorRendersIdenticallyToHexColour(t *testing.T) {
	for _, profile := range []termenv.Profile{termenv.ANSI256, termenv.ANSI, termenv.TrueColor} {
		r := lipgloss.NewRenderer(io.Discard)
		r.SetColorProfile(profile)
		for _, hex := range nickPalette {
			slow := r.NewStyle().Foreground(lipgloss.Color(hex)).Bold(true).Render("x")
			fast := r.NewStyle().Foreground(fastColor(r, hex)).Bold(true).Render("x")
			if slow != fast {
				t.Errorf("profile %v colour %s: fast %q != slow %q", profile, hex, fast, slow)
			}
		}
	}
}

func TestScrollbackRowsAndPaging(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard)
	m := Model{sty: newStyles(r), textinput: textinput.New(), width: 40, height: 10} // 6 visible rows
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	line := func(i int) hub.Line {
		return hub.Line{Time: at, Kind: hub.KindChat, Sender: "bob", Body: "msg " + strconv.Itoa(i)}
	}

	for i := 0; i < 3; i++ { // a short conversation is bottom-anchored under blank rows
		m.appendLine(line(i))
	}
	vis := m.visibleRows()
	if len(vis) != 6 || vis[0] != "" || vis[2] != "" || !strings.Contains(vis[5], "msg 2") || !strings.Contains(vis[3], "msg 0") {
		t.Fatalf("short conversation not bottom-anchored: %q", vis)
	}

	for i := 3; i < 20; i++ {
		m.appendLine(line(i))
	}
	vis = m.visibleRows()
	if len(vis) != 6 || !strings.Contains(vis[5], "msg 19") || !strings.Contains(vis[0], "msg 14") {
		t.Fatalf("window should show the last 6 rows: %q", vis)
	}

	m.pageUp()
	vis = m.visibleRows()
	if !strings.Contains(vis[5], "msg 14") {
		t.Fatalf("pageUp should scroll back about a screenful: %q", vis)
	}
	m.pageUp()
	m.pageUp()
	m.pageUp()
	m.pageUp()
	if vis = m.visibleRows(); !strings.Contains(vis[0], "msg 0") {
		t.Fatalf("pageUp must stop at the oldest message, not run past it: %q", vis)
	}
	m.pageDown()
	m.pageDown()
	m.pageDown()
	m.pageDown()
	m.pageDown()
	if vis = m.visibleRows(); !strings.Contains(vis[5], "msg 19") {
		t.Fatalf("pageDown should return to the newest message: %q", vis)
	}
	m.pageUp()
	m.appendLine(line(20))
	if m.scroll != 0 || !strings.Contains(m.visibleRows()[5], "msg 20") {
		t.Fatal("a new message should return the view to the bottom")
	}

	// Trimming keeps lines, per-line row counts and rows consistent.
	for i := 21; i < 2200; i++ {
		m.appendLine(line(i))
	}
	total := 0
	for _, n := range m.rowsPer {
		total += n
	}
	if len(m.lines) != maxScrollbackLines || len(m.rowsPer) != maxScrollbackLines || len(m.rows) != total {
		t.Fatalf("scrollback bookkeeping drifted: lines=%d rowsPer=%d rows=%d (sum %d)", len(m.lines), len(m.rowsPer), len(m.rows), total)
	}
	if !strings.Contains(m.visibleRows()[5], "msg 2199") {
		t.Fatal("newest message missing after trimming")
	}
}

// The escape-sequence fast path must produce exactly what lipgloss produces
// for every kind of line, on every colour profile.
func TestFastRowsMatchLipglossOutput(t *testing.T) {
	at := time.Date(2026, 10, 3, 22, 45, 0, 0, time.UTC)
	for _, profile := range []termenv.Profile{termenv.ANSI256, termenv.ANSI, termenv.TrueColor, termenv.Ascii} {
		r := lipgloss.NewRenderer(io.Discard)
		r.SetColorProfile(profile)
		sty := newStyles(r)
		nick := sty.NickStyle("bob")
		pm := func(dir string) string {
			return sty.PMLine.Render("[PM "+dir) + " " + nick.Render("bob") + sty.PMLine.Render("]")
		}
		ts := sty.Timestamp.Render("[22:45]")
		cases := []struct {
			line hub.Line
			want string
		}{
			{hub.Line{Time: at, Kind: hub.KindChat, Sender: "bob", Body: "hello there"}, ts + " " + nick.Render("bob") + ": " + sty.ChatBody.Render("hello there")},
			{hub.Line{Time: at, Kind: hub.KindAction, Sender: "bob", Body: "waves"}, ts + " * " + nick.Render("bob") + " " + sty.ChatBody.Render("waves")},
			{hub.Line{Time: at, Kind: hub.KindSystem, Body: "*** x joined ***"}, ts + " " + sty.SystemLine.Render("*** x joined ***")},
			{hub.Line{Time: at, Kind: hub.KindError, Body: "nope"}, ts + " " + sty.ErrorLine.Render("nope")},
			{hub.Line{Time: at, Kind: hub.KindAdmin, Body: "[ADMIN] hi"}, ts + " " + sty.AdminLine.Render("[ADMIN] hi")},
			{hub.Line{Time: at, Kind: hub.KindInfo, Body: "info text"}, ts + " " + sty.InfoLine.Render("info text")},
			{hub.Line{Time: at, Kind: hub.KindPM, Sender: "bob", Dir: "from", Body: "psst"}, ts + " " + pm("from") + " " + sty.ChatBody.Render("psst")},
			{hub.Line{Time: at, Kind: hub.KindPM, Sender: "bob", Dir: "to", Body: "psst"}, ts + " " + pm("to") + " " + sty.ChatBody.Render("psst")},
		}
		for _, c := range cases {
			rows := renderRows(c.line, sty, 100)
			if len(rows) != 1 || rows[0] != c.want {
				t.Errorf("profile %v kind %s:\n got  %q\n want %q", profile, c.line.Kind, rows, c.want)
			}
		}
	}
}

// The hand-built status bar must look like the lipgloss one: same text, same
// width, coloured.
func TestStatusBarFastPathKeepsWidthAndText(t *testing.T) {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	sty := newStyles(r)
	st := statusBarState{Now: time.Date(2026, 10, 3, 22, 45, 7, 0, time.UTC), Room: hub.RoomInfo{Name: "main", Members: 3}, TotalOnline: 9, StartedAt: time.Now()}
	for _, w := range []int{60, 80, 120, 25} {
		got := renderStatusBar(st, w, sty)
		if lipgloss.Width(got) != w {
			t.Errorf("width %d: bar is %d wide", w, lipgloss.Width(got))
		}
		if !strings.Contains(got, "\x1b[") {
			t.Errorf("width %d: bar lost its colours", w)
		}
		want := sty.StatusBar.Width(w).Render(strings.TrimSpace(ansiStrip(got)))
		if ansiStrip(want) != ansiStrip(got) {
			t.Errorf("width %d: text differs from lipgloss:\n got  %q\n want %q", w, ansiStrip(got), ansiStrip(want))
		}
	}
}

func ansiStrip(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && !(s[i] >= 'A' && s[i] <= 'Z' || s[i] >= 'a' && s[i] <= 'z') {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
