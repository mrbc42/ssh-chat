package ui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func withBanner(t *testing.T, b BannerConfig) {
	t.Helper()
	old := banner
	SetBanner(b)
	t.Cleanup(func() { SetBanner(old) })
}

func bannerModel(profile termenv.Profile, width int) Model {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(profile)
	return Model{sty: newStyles(r), width: width, height: 20}
}

// With no configuration the banner must look exactly as it always did.
func TestDefaultBannerIsUnchanged(t *testing.T) {
	cfg, err := ParseBanner("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	withBanner(t, cfg)
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.Ascii} {
		m := bannerModel(profile, 80)
		r := lipgloss.NewRenderer(io.Discard)
		r.SetColorProfile(profile)
		want := r.NewStyle().Background(fastColor(r, "#5f00af")).Foreground(fastColor(r, "#ffd700")).Bold(true).
			Width(80).Align(lipgloss.Center).Render("S S H - C H A T   B B S")
		if got := m.renderBanner(); got != want {
			t.Errorf("profile %v: default banner changed:\n got  %q\n want %q", profile, got, want)
		}
	}
}

func TestBannerTextAndColoursCanBeChanged(t *testing.T) {
	cfg, err := ParseBanner("Gus's Garage BBS", "#0a5", "#FFFFFF")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BG != "#00aa55" || cfg.FG != "#ffffff" {
		t.Fatalf("colours should be normalised to #rrggbb: %+v", cfg)
	}
	withBanner(t, cfg)
	m := bannerModel(termenv.TrueColor, 60)
	got := m.renderBanner()
	if !strings.Contains(got, "Gus's Garage BBS") || strings.Contains(got, "C H A T") {
		t.Fatalf("custom text not shown: %q", ansiStrip(got))
	}
	if !strings.Contains(got, "48;2;0;170;85") { // background #00aa55
		t.Fatalf("custom background colour not applied: %q", got)
	}
	if !strings.Contains(got, "38;2;255;255;255") { // text #ffffff
		t.Fatalf("custom text colour not applied: %q", got)
	}
	if strings.Contains(got, "95;0;175") { // the old purple
		t.Fatal("the old purple is still in use")
	}
	if lipgloss.Width(got) != 60 {
		t.Fatalf("banner should span the width: %d", lipgloss.Width(got))
	}
}

func TestBannerSettingsAreValidated(t *testing.T) {
	for _, c := range []struct{ text, bg, fg string }{
		{"", "red", ""}, {"", "#12", ""}, {"", "#gggggg", ""}, {"", "5f00af", ""}, {"", "", "#12345"}, {"", "", "rgb(1,2,3)"},
	} {
		if _, err := ParseBanner(c.text, c.bg, c.fg); err == nil {
			t.Errorf("%+v should be rejected", c)
		}
	}
	b, err := ParseBanner("   ", "  ", "")
	if err != nil || b.Text != DefaultBannerText || b.BG != DefaultBannerBG || b.FG != DefaultBannerFG {
		t.Fatalf("blank values mean the defaults: %+v err=%v", b, err)
	}
	b, _ = ParseBanner("hello\x1b[31m world\r\n!", "", "")
	if strings.ContainsAny(b.Text, "\x1b\r\n") {
		t.Fatalf("control characters must be stripped: %q", b.Text)
	}
	b, _ = ParseBanner(strings.Repeat("x", 500), "", "")
	if n := len([]rune(b.Text)); n != maxBannerRunes {
		t.Fatalf("text should be capped at %d, got %d", maxBannerRunes, n)
	}
}

// The banner is a single row whatever the text, so the layout can't be broken.
func TestLongBannerIsTrimmedToTheTerminalWidth(t *testing.T) {
	cfg, _ := ParseBanner(strings.Repeat("WIDE ", 20), "", "")
	withBanner(t, cfg)
	for _, w := range []int{20, 40, 80} {
		m := bannerModel(termenv.ANSI256, w)
		got := m.renderBanner()
		if strings.Contains(got, "\n") || lipgloss.Width(got) != w {
			t.Errorf("width %d: banner must be one row of exactly %d columns, got %d wide, newline=%v", w, w, lipgloss.Width(got), strings.Contains(got, "\n"))
		}
	}
	if got := fitBanner("héllo wörld", 5); got != "héllo" {
		t.Fatalf("fitBanner = %q", got)
	}
}
