package ui

import (
	"hash/fnv"
	"strconv"
	"strings"
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

// ansiStyle is a lipgloss style reduced to the escape sequences it emits, so
// the hot path (styling every chat line, on every channel hop, for every user)
// is two string concatenations instead of a full lipgloss Render. Profiling at
// 1000 users showed lipgloss Render and the copying of its large Style struct
// were ~70% of the server's CPU.
type ansiStyle struct{ open, close string }

// fastOf derives the open/close sequences by rendering a sentinel once. Only
// valid for single-line, single-run text (which is all we apply it to).
func fastOf(st lipgloss.Style) ansiStyle {
	const mark = "\x00"
	out := st.Render(mark)
	if open, close, ok := strings.Cut(out, mark); ok {
		return ansiStyle{open, close}
	}
	return ansiStyle{}
}

func (a ansiStyle) apply(s string) string {
	if a.open == "" || s == "" {
		return s
	}
	return a.open + s + a.close
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

	// Precomputed escape-sequence forms of the styles used per chat line.
	fTimestamp, fChat, fError, fInfo, fSystem, fAdmin, fPM, fBar ansiStyle
	fNick                                                        []ansiStyle
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
		st := r.NewStyle().Foreground(c(hex)).Bold(true)
		s.nickSlots = append(s.nickSlots, st)
		s.fNick = append(s.fNick, fastOf(st))
	}
	s.fTimestamp, s.fChat, s.fError, s.fInfo = fastOf(s.Timestamp), fastOf(s.ChatBody), fastOf(s.ErrorLine), fastOf(s.InfoLine)
	s.fSystem, s.fAdmin, s.fPM = fastOf(s.SystemLine), fastOf(s.AdminLine), fastOf(s.PMLine)
	s.fBar = fastOf(s.StatusBar.UnsetPadding()) // padding is added by hand in renderStatusBar
	return s
}

// nickFast is the escape-sequence form of a nickname's colour.
func (s *styles) nickFast(nick string) ansiStyle { return s.fNick[nickIndex(nick)] }

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
