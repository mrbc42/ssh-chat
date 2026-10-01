package ui

import (
	"hash/fnv"

	"github.com/charmbracelet/lipgloss"
)

// nickPalette is the set of bright, high-contrast colors nicknames are
// hashed into — enough distinct hues that two people chatting together
// almost always get visibly different colors, old-BBS-terminal style.
var nickPalette = []string{
	"#00d7ff", // bright cyan
	"#ff5fd7", // bright magenta/pink
	"#ffd700", // bright yellow
	"#5fff5f", // bright green
	"#ff8700", // bright orange
	"#5f87ff", // bright blue
	"#ff5f5f", // bright red
	"#d7ff5f", // lime
	"#af87ff", // lavender
	"#5fffd7", // mint
}

func colorForNick(nick string) lipgloss.Color {
	h := fnv.New32a()
	_, _ = h.Write([]byte(nick))
	return lipgloss.Color(nickPalette[h.Sum32()%uint32(len(nickPalette))])
}

type styles struct {
	Banner      lipgloss.Style
	StatusBar   lipgloss.Style
	InputPrompt lipgloss.Style
	Timestamp   lipgloss.Style
	ChatBody    lipgloss.Style
	ErrorLine   lipgloss.Style
	InfoLine    lipgloss.Style
	SystemLine  lipgloss.Style
	AdminLine   lipgloss.Style
	TooNarrow   lipgloss.Style

	renderer *lipgloss.Renderer
}

func newStyles(r *lipgloss.Renderer) styles {
	return styles{
		Banner:      r.NewStyle().Background(lipgloss.Color("#5f00af")).Foreground(lipgloss.Color("#ffd700")).Bold(true),
		StatusBar:   r.NewStyle().Background(lipgloss.Color("#005f87")).Foreground(lipgloss.Color("#ffffff")).Bold(true).Padding(0, 1),
		InputPrompt: r.NewStyle().Foreground(lipgloss.Color("#5fff5f")).Bold(true),
		Timestamp:   r.NewStyle().Foreground(lipgloss.Color("#767676")),
		ChatBody:    r.NewStyle().Foreground(lipgloss.Color("#e4e4e4")),
		ErrorLine:   r.NewStyle().Foreground(lipgloss.Color("#ff5f5f")).Bold(true),
		InfoLine:    r.NewStyle().Foreground(lipgloss.Color("#00d7ff")),
		SystemLine:  r.NewStyle().Foreground(lipgloss.Color("#ffd700")).Italic(true),
		AdminLine:   r.NewStyle().Foreground(lipgloss.Color("#ff5fd7")).Bold(true).Reverse(true),
		TooNarrow:   r.NewStyle().Foreground(lipgloss.Color("#ff5f5f")).Bold(true),
		renderer:    r,
	}
}

func (s styles) NickStyle(nick string) lipgloss.Style {
	return s.renderer.NewStyle().Foreground(colorForNick(nick)).Bold(true)
}
