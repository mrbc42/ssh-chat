package ui

import (
	"context"
	"fmt"
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
	roomInfo hub.RoomInfo

	width, height int
}

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
		ctx:       ctx,
		h:         h,
		sess:      sess,
		renderer:  renderer,
		sty:       sty,
		viewport:  vp,
		textinput: ti,
		width:     width,
		height:    height,
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

func (m *Model) layout() {
	m.viewport.Width = m.width
	m.viewport.Height = max(m.height-fixedRows, 1)
	m.textinput.Width = max(m.width-2, 1)
	m.viewport.SetContent(renderLines(m.lines, m.sty, m.viewport.Width, m.viewport.Height))
	m.viewport.GotoBottom()
}

func (m *Model) appendLine(line hub.Line) {
	m.lines = append(m.lines, line)
	if len(m.lines) > 2000 {
		m.lines = m.lines[len(m.lines)-2000:]
	}
	m.viewport.SetContent(renderLines(m.lines, m.sty, m.viewport.Width, m.viewport.Height))
	m.viewport.GotoBottom()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		return m, nil

	case tickMsg:
		return m, tickCmd()

	case outboundMsg:
		ob := msg.ob
		if ob.Disconnect {
			return m, tea.Quit
		}
		if ob.SwitchRoom != nil {
			m.roomInfo = *ob.SwitchRoom
			m.lines = append([]hub.Line{}, ob.Scrollback...)
			m.layout()
		} else if ob.Line != nil {
			m.appendLine(*ob.Line)
		}
		return m, waitForOutbox(m.sess)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "pgup":
			m.viewport.ViewUp()
			return m, nil
		case "pgdown":
			m.viewport.ViewDown()
			return m, nil
		case "enter":
			value := m.textinput.Value()
			m.textinput.Reset()
			if value != "" {
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
	if m.width < minUsableWidth || m.height < minUsableHeight {
		return m.sty.TooNarrow.Render("Terminal too small. Please resize your window.")
	}
	banner := m.sty.Banner.Width(m.width).Align(lipgloss.Center).Render(bannerText)
	bar := renderStatusBar(statusBarState{
		Now:         time.Now(),
		Room:        m.roomInfo,
		TotalOnline: m.h.TotalUsersOnline(),
		StartedAt:   m.h.StartedAt(),
	}, m.width, m.sty)

	return lipgloss.JoinVertical(lipgloss.Left,
		banner,
		m.viewport.View(),
		"",
		bar,
		m.textinput.View(),
	)
}

// renderLines colorizes each logical Line by Kind/sender, word-wraps to
// width, and pads blank rows at the TOP so the conversation stays anchored
// to the bottom of the pane and grows upward as new lines arrive — like an
// old BBS chat window — instead of starting at the top with empty space
// below.
func renderLines(lines []hub.Line, sty styles, width, height int) string {
	var rows []string
	for _, l := range lines {
		rendered := renderOneLine(l, sty)
		if width > 0 {
			rendered = sty.renderer.NewStyle().Width(width).Render(rendered)
		}
		rows = append(rows, strings.Split(rendered, "\n")...)
	}
	if pad := height - len(rows); pad > 0 {
		rows = append(make([]string, pad), rows...)
	}
	return strings.Join(rows, "\n")
}

func renderOneLine(l hub.Line, sty styles) string {
	ts := sty.Timestamp.Render("[" + l.Time.Format("15:04") + "]")
	switch l.Kind {
	case hub.KindChat:
		nick := sty.NickStyle(l.Sender).Render(l.Sender)
		return fmt.Sprintf("%s %s: %s", ts, nick, sty.ChatBody.Render(l.Body))
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
