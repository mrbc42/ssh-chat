package main

import (
	"flag"
	"fmt"
	"strings"
)

// envName maps a flag name to its environment variable:
// "max-conns-per-ip" -> "SSHCHAT_MAX_CONNS_PER_IP".
func envName(flagName string) string {
	return "SSHCHAT_" + strings.ToUpper(strings.ReplaceAll(flagName, "-", "_"))
}

// applyEnv sets every flag from its SSHCHAT_* environment variable, if present
// and non-empty. Call it before fs.Parse: command-line flags then override the
// environment, which overrides the built-in defaults — the usual precedence for
// containers, where a .env / env_file supplies the settings. It returns the
// names of the variables it used (for the startup log).
func applyEnv(fs *flag.FlagSet, lookup func(string) (string, bool)) ([]string, error) {
	var used []string
	var firstErr error
	fs.VisitAll(func(f *flag.Flag) {
		name := envName(f.Name)
		v, ok := lookup(name)
		if !ok || strings.TrimSpace(v) == "" {
			return
		}
		if err := f.Value.Set(strings.TrimSpace(v)); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s=%q: %w", name, v, err)
			}
			return
		}
		used = append(used, name)
	})
	return used, firstErr
}

// splitList splits a comma/whitespace separated list, dropping empties.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}
