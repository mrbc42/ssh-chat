package ui

import "strings"

const maxMotdBoxWidth = 76

// motdBox draws the message of the day as a red bounding box with "MOTD" set
// into the top border at the left:
//
//	┌─ MOTD ─────────────────────────────┐
//	│ Server maintenance Sunday 2am.     │
//	└────────────────────────────────────┘
//
// The box is at most maxMotdBoxWidth wide (or the terminal width, if that is
// narrower), the text wraps inside it, and on a terminal too narrow for a box
// it falls back to a plain "MOTD: text" line.
func motdBox(text string, sty *styles, width int) []string {
	if width < 16 {
		return []string{sty.fBox.apply("MOTD:") + " " + sty.fChat.apply(text)}
	}
	w := min(width, maxMotdBoxWidth)
	inner := w - 4 // "│ " + text + " │"
	var rows []string

	label := " MOTD "
	dashes := w - 2 - len(label) - 1 // "┌─" + label + dashes + "┐" must total w columns
	top := "┌─" + label + strings.Repeat("─", max(dashes, 0)) + "┐"
	rows = append(rows, sty.fBox.apply(top))

	lines := wrapPlain(text, inner)
	if len(lines) == 0 {
		lines = []string{""}
	}
	side := sty.fBox.apply("│")
	for _, l := range lines {
		pad := inner - textWidth(l)
		rows = append(rows, side+" "+sty.fChat.apply(l)+strings.Repeat(" ", max(pad, 0))+" "+side)
	}
	rows = append(rows, sty.fBox.apply("└"+strings.Repeat("─", w-2)+"┘"))
	return rows
}
