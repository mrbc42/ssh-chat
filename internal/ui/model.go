package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
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
	sty      styles

	viewport  viewport.Model
	textinput textinput.Model

	lines    []hub.Line
	rowsPer  [][]string // rendered, wrapped rows for each entry of lines (parallel slice)
	roomInfo hub.RoomInfo

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

	vp := viewport.New(width, max(height-fixedRows, 1))

	return Model{
		totalOnline: h.TotalUsersOnline(),
		ctx:         ctx,
		h:           h,
		sess:        sess,
		renderer:    renderer,
		sty:         sty,
		viewport:    vp,
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
	m.textinput.Prompt = nick + "> "
	m.textinput.PromptStyle = m.sty.NickStyle(nick)
	m.textinput.Width = max(m.width-lipgloss.Width(m.textinput.Prompt)-1, 1)
}

// layout re-renders every line for the current size. It is only needed when
// the size or the set of lines changes wholesale (resize, room switch,
// /clear); a single new message goes through appendLine instead.
func (m *Model) layout() {
	m.viewport.Width = m.width
	m.viewport.Height = max(m.height-fixedRows, 1)
	m.syncPrompt()
	m.rowsPer = make([][]string, len(m.lines))
	for i, l := range m.lines {
		m.rowsPer[i] = renderRows(l, m.sty, m.viewport.Width)
	}
	m.refreshViewport()
}

// refreshViewport rebuilds the pane from the cached rows (cheap: no styling)
// and keeps the conversation bottom-anchored.
func (m *Model) refreshViewport() {
	var rows []string
	for _, r := range m.rowsPer {
		rows = append(rows, r...)
	}
	if pad := m.viewport.Height - len(rows); pad > 0 {
		rows = append(make([]string, pad), rows...)
	}
	m.viewport.SetContent(strings.Join(rows, "\n"))
	m.viewport.GotoBottom()
}

func (m *Model) appendLine(line hub.Line) {
	m.lines = append(m.lines, line)
	m.rowsPer = append(m.rowsPer, renderRows(line, m.sty, m.viewport.Width)) // only the new line is styled
	if len(m.lines) > 2000 {
		m.lines = m.lines[len(m.lines)-2000:]
		m.rowsPer = m.rowsPer[len(m.rowsPer)-2000:]
	}
	m.refreshViewport()
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
			m.viewport.ViewUp()
			return m, nil
		case "pgdown":
			m.viewport.ViewDown()
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
	banner := m.sty.Banner.Width(m.width).Align(lipgloss.Center).Render(bannerText)
	bar := renderStatusBar(statusBarState{
		Now:         time.Now(),
		Room:        m.roomInfo,
		TotalOnline: m.totalOnline,
		StartedAt:   m.h.StartedAt(),
	}, m.width, m.sty)

	out := lipgloss.JoinVertical(lipgloss.Left,
		banner,
		m.viewport.View(),
		"",
		bar,
		m.textinput.View(),
	)
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

// renderRows styles one chat line and word-wraps it to width.
func renderRows(l hub.Line, sty styles, width int) []string {
	rendered := renderOneLine(l, sty)
	if width > 0 {
		rendered = sty.renderer.NewStyle().Width(width).Render(rendered)
	}
	return strings.Split(rendered, "\n")
}

func renderOneLine(l hub.Line, sty styles) string {
	ts := sty.Timestamp.Render("[" + l.Time.Format("15:04") + "]")
	switch l.Kind {
	case hub.KindChat:
		nick := sty.NickStyle(l.Sender).Render(l.Sender)
		return fmt.Sprintf("%s %s: %s", ts, nick, sty.ChatBody.Render(l.Body))
	case hub.KindAction:
		nick := sty.NickStyle(l.Sender).Render(l.Sender)
		return fmt.Sprintf("%s * %s %s", ts, nick, sty.ChatBody.Render(l.Body))
	case hub.KindSystem:
		return ts + " " + sty.SystemLine.Render(l.Body)
	case hub.KindError:
		return ts + " " + sty.ErrorLine.Render(l.Body)
	case hub.KindAdmin:
		return ts + " " + sty.AdminLine.Render(l.Body)
	case hub.KindPM:
		arrow := "from"
		if l.Dir == "to" {
			arrow = "to"
		}
		nick := sty.NickStyle(l.Sender).Render(l.Sender)
		prefix := sty.PMLine.Render(fmt.Sprintf("[PM %s", arrow)) + " " + nick + sty.PMLine.Render("]")
		return fmt.Sprintf("%s %s %s", ts, prefix, sty.ChatBody.Render(l.Body))
	default: // KindInfo
		return ts + " " + sty.InfoLine.Render(l.Body)
	}
}
