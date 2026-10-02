// Package filter masks blocklisted words in chat text.
package filter

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// defaultWords is a deliberately short list of unambiguous profanity and
// slurs. Short substrings that appear inside innocent words are avoided
// because matching is on whole words.
var defaultWords = []string{
	"fuck", "shit", "bitch", "cunt", "asshole", "bastard", "dickhead", "piss",
	"slut", "whore", "wanker", "twat", "nigger", "faggot", "retard",
}

// Filter masks whole-word matches (plus common endings like -s, -ing, -ed,
// -y) as the first letter followed by asterisks. It does not try to catch
// deliberate obfuscation such as l33tspeak or spacing.
type Filter struct {
	re *regexp.Regexp
}

// New builds a Filter from the default list plus extra words.
func New(extra []string) *Filter {
	seen := map[string]bool{}
	var parts []string
	for _, w := range append(append([]string{}, defaultWords...), extra...) {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		parts = append(parts, regexp.QuoteMeta(w))
	}
	return &Filter{re: regexp.MustCompile(`(?i)\b(?:` + strings.Join(parts, "|") + `)(?:s|es|ed|er|ers|ing|y)?\b`)}
}

// LoadWords reads one word per line ('#' comments and blanks ignored).
func LoadWords(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, sc.Err()
}

// Mask returns s with every blocklisted word masked.
func (f *Filter) Mask(s string) string {
	if f == nil {
		return s
	}
	return f.re.ReplaceAllStringFunc(s, func(m string) string {
		r := []rune(m)
		return string(r[0]) + strings.Repeat("*", len(r)-1)
	})
}
