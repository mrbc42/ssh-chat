package ui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/mrbc42/ssh-chat/internal/hub"
)

const minUsableWidth = 20
const minUsableHeight = 6

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

	lines    []string
	roomInfo hub.RoomInfo

	width, height int
}

func NewModel(ctx context.Context, h *hub.Hub, sess *hub.Session, renderer *lipgloss.Renderer, width, height int) Model {
	ti := textinput.New()
	ti.Placeholder = "Type a message, or /help for commands..."
	ti.Focus()
	ti.CharLimit = 600
	ti.Prompt = "> "

	vp := viewport.New(width, max(height-3, 1))

	return Model{
		ctx:       ctx,
		h:         h,
		sess:      sess,
		renderer:  renderer,
		sty:       newStyles(renderer),
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
	m.viewport.Height = max(m.height-3, 1)
	m.textinput.Width = max(m.width-2, 1)
	m.viewport.SetContent(renderLines(m.lines, m.sty))
	m.viewport.GotoBottom()
}

func (m *Model) appendLine(line string) {
	m.lines = append(m.lines, line)
	if len(m.lines) > 2000 {
		m.lines = m.lines[len(m.lines)-2000:]
	}
	m.viewport.SetContent(renderLines(m.lines, m.sty))
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
			m.lines = nil
			m.lines = append(m.lines, ob.Scrollback...)
			m.layout()
		} else if ob.Line != "" {
			m.appendLine(ob.Line)
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
	bar := renderStatusBar(statusBarState{
		Now:         time.Now(),
		Room:        m.roomInfo,
		TotalOnline: m.h.TotalUsersOnline(),
		StartedAt:   m.h.StartedAt(),
	}, m.width, m.sty)

	return lipgloss.JoinVertical(lipgloss.Left,
		m.viewport.View(),
		bar,
		m.textinput.View(),
	)
}

func renderLines(lines []string, sty styles) string {
	rendered := make([]string, len(lines))
	for i, l := range lines {
		if strings.Contains(l, "***") {
			rendered[i] = sty.SystemLine.Render(l)
		} else {
			rendered[i] = l
		}
	}
	return strings.Join(rendered, "\n")
}
