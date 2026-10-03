package ui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mrbc42/ssh-chat/internal/hub"
)

const minUsableWidth = 20
const minUsableHeight = 6
const bannerText = "S S H - C H A T   B B S"
const fixedRows = 4 // banner + blank separator + status bar + input line

type outboundMsg struct{ ob hub.Outbound }
type tickMsg time.Time

type Model struct {
	ctx  context.Context
	h    *hub.Hub
	sess *hub.Session

	renderer *lipgloss.Renderer
	sty      *styles

	textinput textinput.Model

	// Scrollback. lines are the raw chat lines; rows is the same content
	// already styled and wrapped, flattened, so showing a screenful or adding
	// one message never re-styles anything old. rowsPer[i] is how many rows
	// line i occupies (so the oldest line can be dropped). scroll is how many
	// rows up from the bottom the user has paged (0 = following the chat).
	lines    []hub.Line
	rows     []string
	rowsPer  []int
	scroll   int
	roomInfo hub.RoomInfo

	// Pre-rendered pieces that only change on resize / once a second / room
	// switch, not on every message.
	banner, bar string
	lastNick    string

	// totalOnline is refreshed once a second, never inside View: counting
	// users is a blocking round trip to every room's goroutine, and View runs
	// on every redraw of every connected user.
	totalOnline int

	width, height int

	// focused tracks terminal focus (via DEC 1004 focus reporting, when the
	// client terminal supports it); pendingBell rings the bell for exactly
	// one render after an alert-worthy line arrives while unfocused.
	focused     bool
	pendingBell bool

	// tab-completion cycling state for repeated Tab presses on an
	// ambiguous command prefix; cleared on any other keypress.
	tabMatches []string
	tabIndex   int

	// Up/Down input history, like a shell: history holds what this user
	// has submitted (oldest first); histPos indexes into it while browsing
	// and equals len(history) when not browsing. draft keeps the
	// half-typed line so Down past the newest entry restores it.
	history []string
	histPos int
	draft   string
}

const maxHistory = 100

func NewModel(ctx context.Context, h *hub.Hub, sess *hub.Session, renderer *lipgloss.Renderer, width, height int) Model {
	sty := newStyles(renderer)

	ti := textinput.New()
	ti.Placeholder = "Type a message, or /help for commands..."
	ti.CharLimit = 600
	ti.Prompt = "> "
	// Focus here, at construction, not in Init(): Init has a value receiver
	// and cannot mutate the Model the tea.Program actually holds, so a
	// Focus() call made there is silently lost and every keystroke gets
	// dropped by textinput's own "not focused" guard.
	ti.Focus()
	ti.PromptStyle = sty.InputPrompt
	// A visibly blinking block cursor, old-terminal style: reversed bright
	// green so it reads clearly against the dark background.
	ti.Cursor.Style = renderer.NewStyle().Foreground(lipgloss.Color("#5fff5f")).Reverse(true)
	ti.Cursor.TextStyle = renderer.NewStyle().Foreground(lipgloss.Color("#e4e4e4"))
	ti.Cursor.SetMode(cursor.CursorBlink)

	return Model{
		totalOnline: h.TotalUsersOnline(),
		ctx:         ctx,
		h:           h,
		sess:        sess,
		renderer:    renderer,
		sty:         sty,
		textinput:   ti,
		width:       width,
		height:      height,
		focused:     true,
	}
}

func waitForOutbox(sess *hub.Session) tea.Cmd {
	return func() tea.Msg {
		ob, ok := <-sess.Outbox
		if !ok {
			return outboundMsg{ob: hub.Outbound{Disconnect: true}}
		}
		return outboundMsg{ob: ob}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, waitForOutbox(m.sess), tickCmd())
}

// syncPrompt makes the input prompt "<nick>> " in the same colour as the
// user's name in the chat pane, like a shell prompt, and keeps the input
// width in step with the (variable-length) prompt. The nick can change at
// any time via /nick, so this runs on every update and render.
func (m *Model) syncPrompt() {
	if m.sess == nil {
		return
	}
	nick := m.sess.Nick()
	if nick != m.lastNick || m.textinput.Width == 0 {
		m.lastNick = nick
		m.textinput.Prompt = nick + "> "
		m.textinput.PromptStyle = m.sty.NickStyle(nick)
		m.textinput.Width = max(m.width-lipgloss.Width(m.textinput.Prompt)-1, 1)
	}
}

func (m *Model) viewHeight() int { return max(m.height-fixedRows, 1) }

// layout re-renders every line for the current size. It is only needed when
// the size or the set of lines changes wholesale (resize, room switch,
// /clear); a single new message goes through appendLine instead.
func (m *Model) layout() {
	m.lastNick = "" // force the prompt width to be recomputed for the new size
	m.syncPrompt()
	m.banner = m.sty.Banner.Width(m.width).Align(lipgloss.Center).Render(bannerText)
	m.rows, m.rowsPer, m.scroll = nil, make([]int, len(m.lines)), 0
	for i, l := range m.lines {
		r := renderRows(l, m.sty, m.width)
		m.rowsPer[i] = len(r)
		m.rows = append(m.rows, r...)
	}
	m.refreshBar()
}

// refreshBar rebuilds the status bar. Called once a second and on room
// changes, never per message.
func (m *Model) refreshBar() {
	m.bar = renderStatusBar(statusBarState{
		Now:         time.Now(),
		Room:        m.roomInfo,
		TotalOnline: m.totalOnline,
		StartedAt:   m.h.StartedAt(),
	}, m.width, m.sty)
}

const maxScrollbackLines = 2000

func (m *Model) appendLine(line hub.Line) {
	r := renderRows(line, m.sty, m.width) // only the new line is styled
	m.lines = append(m.lines, line)
	m.rowsPer = append(m.rowsPer, len(r))
	m.rows = append(m.rows, r...)
	if len(m.lines) > maxScrollbackLines {
		drop := m.rowsPer[0]
		m.lines = m.lines[1:]
		m.rowsPer = m.rowsPer[1:]
		m.rows = m.rows[drop:]
		if cap(m.lines) > 4*maxScrollbackLines { // let the backing arrays shrink
			m.lines = append([]hub.Line(nil), m.lines...)
			m.rowsPer = append([]int(nil), m.rowsPer...)
			m.rows = append([]string(nil), m.rows...)
		}
	}
	m.scroll = 0 // follow the conversation
}

// pageUp / pageDown scroll the chat pane by nearly a screenful.
func (m *Model) pageUp() {
	m.scroll = min(m.scroll+max(m.viewHeight()-1, 1), max(len(m.rows)-m.viewHeight(), 0))
}

func (m *Model) pageDown() {
	m.scroll = max(m.scroll-max(m.viewHeight()-1, 1), 0)
}

// visibleRows returns exactly viewHeight rows: the current window onto the
// scrollback, bottom-anchored with blank rows above a short conversation.
func (m *Model) visibleRows() []string {
	h := m.viewHeight()
	end := len(m.rows) - m.scroll
	start := end - h
	out := make([]string, 0, h)
	if start < 0 {
		for i := start; i < 0; i++ {
			out = append(out, "")
		}
		start = 0
	}
	return append(out, m.rows[start:end]...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.syncPrompt()
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		return m, nil

	case tickMsg:
		// Clear any pending bell here rather than via an immediate
		// self-sent message: a zero-latency round-trip command races the
		// (possibly frame-rate-throttled) renderer and can clear the flag
		// before any frame is ever drawn with it set, silently swallowing
		// the beep. Piggybacking on the 1-second tick guarantees at least
		// one render sees it first.
		m.pendingBell = false
		m.totalOnline = m.h.TotalUsersOnline()
		m.refreshBar()
		m.h.CheckIdle(m.sess)
		return m, tickCmd()

	case outboundMsg:
		ob := msg.ob
		if ob.Disconnect {
			return m, tea.Quit
		}
		if ob.Clear {
			m.lines = nil
			m.layout()
		} else if ob.SwitchRoom != nil {
			m.roomInfo = *ob.SwitchRoom
			m.lines = append([]hub.Line{}, ob.Scrollback...)
			m.layout()
		} else if ob.Line != nil {
			m.appendLine(*ob.Line)
			if !m.focused && shouldBeep(*ob.Line, m.sess.Nick()) {
				m.pendingBell = true
			}
		}
		return m, waitForOutbox(m.sess)

	case tea.FocusMsg:
		m.focused = true
		return m, nil

	case tea.BlurMsg:
		m.focused = false
		return m, nil

	case tea.KeyMsg:
		if msg.String() != "tab" {
			m.tabMatches = nil
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "pgup":
			m.pageUp()
			return m, nil
		case "pgdown":
			m.pageDown()
			return m, nil
		case "tab":
			m.completeCommand()
			return m, nil
		case "up":
			m.historyPrev()
			return m, nil
		case "down":
			m.historyNext()
			return m, nil
		case "enter":
			value := m.textinput.Value()
			m.textinput.Reset()
			if value != "" {
				m.addHistory(value)
				hub.HandleInput(m.ctx, m.h, m.sess, value)
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.textinput, cmd = m.textinput.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	m.syncPrompt() // on this render's copy: reflects a /nick made since the last update
	if m.width < minUsableWidth || m.height < minUsableHeight {
		return m.sty.TooNarrow.Render("Terminal too small. Please resize your window.")
	}
	banner, bar := m.banner, m.bar
	if banner == "" { // before the first layout
		banner = m.sty.Banner.Width(m.width).Align(lipgloss.Center).Render(bannerText)
	}
	if bar == "" {
		m.refreshBar()
		bar = m.bar
	}
	// Plain joins: every piece is already the right width, so lipgloss's
	// block-alignment pass (JoinVertical) would only burn CPU.
	out := banner + "\n" + strings.Join(m.visibleRows(), "\n") + "\n\n" + bar + "\n" + m.textinput.View()
	if m.pendingBell {
		// A single BEL byte triggers whatever bell behavior the client
		// terminal has configured (audible, visual flash, or nothing if
		// disabled) — the most a server can do; it can't control how the
		// client presents it.
		return "\a" + out
	}
	return out
}

// shouldBeep reports whether an incoming line is alert-worthy enough to
// ring the bell while the window is unfocused: other people's chat
// messages, incoming PMs, and admin broadcasts — not your own echoed
// messages, join/leave chatter, or command feedback.
func shouldBeep(l hub.Line, myNick string) bool {
	switch l.Kind {
	case hub.KindChat, hub.KindAction:
		return l.Sender != myNick
	case hub.KindPM:
		return l.Dir == "from"
	case hub.KindAdmin:
		return true
	default:
		return false
	}
}

// completeCommand implements Tab completion for the slash-command name
// only (up to the first space); repeated Tab presses on an ambiguous
// prefix cycle through the matches. Cycle state (tabMatches/tabIndex) is
// tracked independently of the input text — not re-derived from it each
// press — since the input text itself changes as we complete, and
// re-deriving a prefix from the now-completed text would misread it as a
// fresh, unambiguous match and break the cycle after one step.
func (m *Model) completeCommand() {
	if len(m.tabMatches) > 0 {
		m.tabIndex = (m.tabIndex + 1) % len(m.tabMatches)
		m.setCompletion(m.tabMatches[m.tabIndex])
		return
	}

	val := m.textinput.Value()
	if !strings.HasPrefix(val, "/") || strings.Contains(val, " ") {
		return
	}
	prefix := strings.ToLower(val[1:])
	if prefix == "" {
		return
	}

	var matches []string
	for _, name := range hub.CommandNames() {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return
	}
	m.tabMatches = matches
	m.tabIndex = 0
	m.setCompletion(matches[0])
}

func (m *Model) addHistory(value string) {
	if n := len(m.history); n == 0 || m.history[n-1] != value {
		m.history = append(m.history, value)
		if len(m.history) > maxHistory {
			m.history = m.history[len(m.history)-maxHistory:]
		}
	}
	m.histPos = len(m.history)
	m.draft = ""
}

func (m *Model) historyPrev() {
	if m.histPos == 0 {
		return
	}
	if m.histPos == len(m.history) {
		m.draft = m.textinput.Value()
	}
	m.histPos--
	m.textinput.SetValue(m.history[m.histPos])
	m.textinput.CursorEnd()
}

func (m *Model) historyNext() {
	if m.histPos >= len(m.history) {
		return
	}
	m.histPos++
	if m.histPos == len(m.history) {
		m.textinput.SetValue(m.draft)
	} else {
		m.textinput.SetValue(m.history[m.histPos])
	}
	m.textinput.CursorEnd()
}

func (m *Model) setCompletion(name string) {
	val := "/" + name
	if len(m.tabMatches) == 1 {
		val += " "
	}
	m.textinput.SetValue(val)
	m.textinput.CursorEnd()
}

// renderRows styles one chat line and word-wraps it to width. Continuation
// rows are indented under the start of the message text, so a wrapped
// sentence reads as one message rather than as several.
func renderRows(l hub.Line, sty *styles, width int) []string {
	prefix, body, style := splitLine(l, sty)
	pw := lipgloss.Width(prefix)
	// Too narrow (or a very long nick) for a hanging indent: wrap flush left.
	if width <= 0 || pw > width/2 {
		rendered := prefix + style.Render(body)
		if width > 0 {
			rendered = sty.renderer.NewStyle().Width(width).Render(rendered)
		}
		return strings.Split(rendered, "\n")
	}
	indent := strings.Repeat(" ", pw)
	var rows []string
	for i, part := range wrapPlain(body, width-pw) {
		if i == 0 {
			rows = append(rows, prefix+style.Render(part))
		} else {
			rows = append(rows, indent+style.Render(part))
		}
	}
	if len(rows) == 0 {
		rows = []string{prefix}
	}
	return rows
}

// wrapPlain word-wraps plain text to width columns, hard-splitting any word
// longer than a row.
func wrapPlain(s string, width int) []string {
	var rows []string
	cur := ""
	for _, w := range strings.Fields(s) {
		for lipgloss.Width(w) > width {
			if cur != "" {
				rows, cur = append(rows, cur), ""
			}
			r := []rune(w)
			n := 0
			for n < len(r) && lipgloss.Width(string(r[:n+1])) <= width {
				n++
			}
			if n == 0 {
				n = 1
			}
			rows, w = append(rows, string(r[:n])), string(r[n:])
		}
		switch {
		case cur == "":
			cur = w
		case lipgloss.Width(cur)+1+lipgloss.Width(w) <= width:
			cur += " " + w
		default:
			rows, cur = append(rows, cur), w
		}
	}
	if cur != "" {
		rows = append(rows, cur)
	}
	return rows
}

// splitLine returns the styled leader ("[15:04] nick: "), the plain message
// text, and the style to apply to each row of that text.
func splitLine(l hub.Line, sty *styles) (prefix, body string, style lipgloss.Style) {
	ts := sty.Timestamp.Render("[" + l.Time.Format("15:04") + "]")
	switch l.Kind {
	case hub.KindChat:
		return ts + " " + sty.NickStyle(l.Sender).Render(l.Sender) + ": ", l.Body, sty.ChatBody
	case hub.KindAction:
		return ts + " * " + sty.NickStyle(l.Sender).Render(l.Sender) + " ", l.Body, sty.ChatBody
	case hub.KindSystem:
		return ts + " ", l.Body, sty.SystemLine
	case hub.KindError:
		return ts + " ", l.Body, sty.ErrorLine
	case hub.KindAdmin:
		return ts + " ", l.Body, sty.AdminLine
	case hub.KindPM:
		arrow := "from"
		if l.Dir == "to" {
			arrow = "to"
		}
		tag := sty.PMLine.Render("[PM "+arrow) + " " + sty.NickStyle(l.Sender).Render(l.Sender) + sty.PMLine.Render("]")
		return ts + " " + tag + " ", l.Body, sty.ChatBody
	default: // KindInfo
		return ts + " ", l.Body, sty.InfoLine
	}
}
