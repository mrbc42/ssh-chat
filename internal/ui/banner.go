package ui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

// The banner is the coloured bar across the top of the chat window. Its text
// and colours are server settings; these are the defaults (the original look).
const (
	DefaultBannerText = "S S H - C H A T   B B S"
	DefaultBannerBG   = "#5f00af" // purple
	DefaultBannerFG   = "#ffd700" // gold
	maxBannerRunes    = 100
)

// BannerConfig is the banner's text, background colour and text colour.
type BannerConfig struct {
	Text string
	BG   string // #rrggbb
	FG   string // #rrggbb
}

var banner = BannerConfig{DefaultBannerText, DefaultBannerBG, DefaultBannerFG}

// SetBanner installs the banner shown to every user. Call once at startup,
// before connections are served.
func SetBanner(b BannerConfig) { banner = b }

var hexColour = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// ParseBanner validates the settings. An empty value means the default. Colours
// are hex (#rrggbb or #rgb); control characters are stripped from the text and
// it is limited to 100 characters (it is also trimmed to the terminal width when
// drawn, so the banner always stays one row).
func ParseBanner(text, bg, fg string) (BannerConfig, error) {
	b := BannerConfig{Text: DefaultBannerText, BG: DefaultBannerBG, FG: DefaultBannerFG}
	if t := cleanBannerText(text); t != "" {
		b.Text = t
	}
	for _, c := range []struct {
		name, in string
		out      *string
	}{{"banner background", bg, &b.BG}, {"banner text colour", fg, &b.FG}} {
		in := strings.TrimSpace(c.in)
		if in == "" {
			continue
		}
		if !hexColour.MatchString(in) {
			return b, fmt.Errorf("%s %q: use a hex colour like #5f00af or #fa0", c.name, c.in)
		}
		*c.out = normaliseHex(in)
	}
	return b, nil
}

// normaliseHex turns #rgb into #rrggbb and lower-cases.
func normaliseHex(h string) string {
	h = strings.ToLower(h)
	if len(h) == 4 {
		return "#" + strings.Repeat(string(h[1]), 2) + strings.Repeat(string(h[2]), 2) + strings.Repeat(string(h[3]), 2)
	}
	return h
}

// cleanBannerText drops control characters (keeping spaces, so spaced-out text
// like the default survives) and caps the length.
func cleanBannerText(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
		if n++; n >= maxBannerRunes {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// fitBanner cuts text to at most width display columns.
func fitBanner(text string, width int) string {
	if width <= 0 || textWidth(text) <= width {
		return text
	}
	var b strings.Builder
	w := 0
	for _, r := range text {
		rw := textWidth(string(r))
		if w+rw > width {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

// renderBanner draws the banner across width columns.
func (m *Model) renderBanner() string {
	return m.sty.Banner.Width(m.width).Align(lipgloss.Center).Render(fitBanner(banner.Text, m.width))
}
