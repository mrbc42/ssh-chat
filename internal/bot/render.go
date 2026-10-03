package bot

import (
	"strings"
	"unicode"
)

// clean makes untrusted text safe to store or echo: control characters
// (including ESC and newlines) become spaces, runs of whitespace collapse,
// and the result is cut to max runes (0 = unlimited).
func clean(s string, max int) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) || r == '​' || r == ' ' || r == ' ' {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
		n++
		if max > 0 && n >= max {
			break
		}
	}
	return b.String()
}

// chunk splits s at word boundaries into pieces of at most max characters
// (long words are hard-split). Normal replies are shorter than max and come
// back as a single piece; the chat screen wraps them to the terminal width.
func chunk(s string, width int) []string {
	var lines []string
	cur := ""
	flush := func() {
		if cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
	}
	for _, w := range strings.Fields(s) {
		for len([]rune(w)) > width {
			flush()
			r := []rune(w)
			lines = append(lines, string(r[:width]))
			w = string(r[width:])
		}
		switch {
		case cur == "":
			cur = w
		case len([]rune(cur))+1+len([]rune(w)) <= width:
			cur += " " + w
		default:
			flush()
			cur = w
		}
	}
	flush()
	return lines
}

// Kinds of output, used only by the colour helper.
const (
	kindPlain = iota
	kindEvent
	kindAnswer
)

// colourise is the single place ANSI colour is applied. With colour off it
// returns the text untouched.
func colourise(on bool, kind int, s string) string {
	if !on {
		return s
	}
	switch kind {
	case kindEvent:
		return "\x1b[36m" + s + "\x1b[0m"
	case kindAnswer:
		return "\x1b[33m" + s + "\x1b[0m"
	}
	return s
}
