package ui

import "github.com/charmbracelet/lipgloss"

type styles struct {
	StatusBar    lipgloss.Style
	StatusField  lipgloss.Style
	InputPrompt  lipgloss.Style
	ErrorLine    lipgloss.Style
	SystemLine   lipgloss.Style
	TooNarrow    lipgloss.Style
}

func newStyles(r *lipgloss.Renderer) styles {
	return styles{
		StatusBar:   r.NewStyle().Background(lipgloss.Color("#2b2b2b")).Foreground(lipgloss.Color("#e0e0e0")).Padding(0, 1),
		StatusField: r.NewStyle().Foreground(lipgloss.Color("#e0e0e0")),
		InputPrompt: r.NewStyle().Foreground(lipgloss.Color("#5fd7ff")).Bold(true),
		ErrorLine:   r.NewStyle().Foreground(lipgloss.Color("#ff5f5f")),
		SystemLine:  r.NewStyle().Foreground(lipgloss.Color("#888888")).Italic(true),
		TooNarrow:   r.NewStyle().Foreground(lipgloss.Color("#ff5f5f")).Bold(true),
	}
}
