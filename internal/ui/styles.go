package ui

import (
	"hash/fnv"
	"strconv"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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

func nickIndex(nick string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(nick))
	return int(h.Sum32() % uint32(len(nickPalette)))
}

// fastColor converts a hex colour to the terminal's own palette ONCE, up front.
// lipgloss otherwise redoes that conversion (an expensive colour-space
// distance search on 256-colour terminals) on every single Render call, which
// profiling showed was a large share of the server's CPU.
func fastColor(r *lipgloss.Renderer, hex string) lipgloss.Color {
	switch c := r.ColorProfile().Color(hex).(type) {
	case termenv.ANSI256Color:
		return lipgloss.Color(strconv.Itoa(int(c)))
	case termenv.ANSIColor:
		return lipgloss.Color(strconv.Itoa(int(c)))
	default: // true colour needs no conversion; a monochrome profile ignores colour anyway
		return lipgloss.Color(hex)
	}
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
	PMLine      lipgloss.Style
	TooNarrow   lipgloss.Style

	renderer *lipgloss.Renderer

	// Nick styles are built once per name and per palette slot, not per line.
	mu         sync.Mutex
	nickStyles map[string]lipgloss.Style
	nickSlots  []lipgloss.Style
}

// newStyles is used through a pointer: the whole struct is large and the UI
// model is copied on every update and render.
func newStyles(r *lipgloss.Renderer) *styles {
	c := func(hex string) lipgloss.Color { return fastColor(r, hex) }
	s := &styles{
		Banner:      r.NewStyle().Background(c("#5f00af")).Foreground(c("#ffd700")).Bold(true),
		StatusBar:   r.NewStyle().Background(c("#005f87")).Foreground(c("#ffffff")).Bold(true).Padding(0, 1),
		InputPrompt: r.NewStyle().Foreground(c("#5fff5f")).Bold(true),
		Timestamp:   r.NewStyle().Foreground(c("#767676")),
		ChatBody:    r.NewStyle().Foreground(c("#e4e4e4")),
		ErrorLine:   r.NewStyle().Foreground(c("#ff5f5f")).Bold(true),
		InfoLine:    r.NewStyle().Foreground(c("#00d7ff")),
		SystemLine:  r.NewStyle().Foreground(c("#ffd700")).Italic(true),
		AdminLine:   r.NewStyle().Foreground(c("#ff5fd7")).Bold(true).Reverse(true),
		PMLine:      r.NewStyle().Foreground(c("#af87ff")).Bold(true),
		TooNarrow:   r.NewStyle().Foreground(c("#ff5f5f")).Bold(true),
		renderer:    r,
		nickStyles:  map[string]lipgloss.Style{},
	}
	for _, hex := range nickPalette {
		s.nickSlots = append(s.nickSlots, r.NewStyle().Foreground(c(hex)).Bold(true))
	}
	return s
}

// NickStyle returns the (cached) chat colour style for a nickname.
func (s *styles) NickStyle(nick string) lipgloss.Style {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.nickStyles[nick]
	if !ok {
		st = s.nickSlots[nickIndex(nick)]
		if len(s.nickStyles) > 4096 {
			s.nickStyles = map[string]lipgloss.Style{} // bound the cache
		}
		s.nickStyles[nick] = st
	}
	return st
}
