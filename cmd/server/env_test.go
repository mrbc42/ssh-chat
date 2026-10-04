package main

import (
	"flag"
	"os"
	"strings"
	"testing"
	"time"
)

func newSet() (*flag.FlagSet, *string, *int, *bool, *time.Duration) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	addr := fs.String("addr", ":2222", "")
	n := fs.Int("max-conns-per-ip", 10, "")
	b := fs.Bool("profanity-filter", true, "")
	d := fs.Duration("afk-after", 5*time.Minute, "")
	return fs, addr, n, b, d
}

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestEnvName(t *testing.T) {
	for in, want := range map[string]string{"addr": "SSHCHAT_ADDR", "max-conns-per-ip": "SSHCHAT_MAX_CONNS_PER_IP", "bot-db": "SSHCHAT_BOT_DB"} {
		if got := envName(in); got != want {
			t.Errorf("envName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEnvOverridesDefaultsAndFlagsOverrideEnv(t *testing.T) {
	fs, addr, n, b, d := newSet()
	used, err := applyEnv(fs, env(map[string]string{
		"SSHCHAT_ADDR":             ":9000",
		"SSHCHAT_MAX_CONNS_PER_IP": " 25 ",
		"SSHCHAT_PROFANITY_FILTER": "false",
		"SSHCHAT_AFK_AFTER":        "7m",
		"SSHCHAT_UNRELATED":        "ignored",
	}))
	if err != nil || len(used) != 4 {
		t.Fatalf("used=%v err=%v", used, err)
	}
	// An explicit command-line flag beats the environment.
	if err := fs.Parse([]string{"-addr", ":7777"}); err != nil {
		t.Fatal(err)
	}
	if *addr != ":7777" || *n != 25 || *b != false || *d != 7*time.Minute {
		t.Fatalf("addr=%q n=%d filter=%v afk=%v", *addr, *n, *b, *d)
	}
}

func TestUnsetAndEmptyEnvKeepDefaults(t *testing.T) {
	fs, addr, n, b, d := newSet()
	used, err := applyEnv(fs, env(map[string]string{"SSHCHAT_ADDR": "", "SSHCHAT_MAX_CONNS_PER_IP": "   "}))
	if err != nil || len(used) != 0 {
		t.Fatalf("empty values must be ignored: used=%v err=%v", used, err)
	}
	if *addr != ":2222" || *n != 10 || !*b || *d != 5*time.Minute {
		t.Fatal("defaults changed")
	}
}

func TestBadEnvValueIsAnError(t *testing.T) {
	fs, _, _, _, _ := newSet()
	_, err := applyEnv(fs, env(map[string]string{"SSHCHAT_MAX_CONNS_PER_IP": "lots"}))
	if err == nil {
		t.Fatal("a non-numeric value must be reported, not silently ignored (main aborts startup on it)")
	}
	if !strings.Contains(err.Error(), "SSHCHAT_MAX_CONNS_PER_IP") {
		t.Fatalf("the error should name the variable: %v", err)
	}
}

func TestSplitList(t *testing.T) {
	got := splitList("SHA256:aaa, SHA256:bbb\nSHA256:ccc ,,")
	if len(got) != 3 || got[1] != "SHA256:bbb" {
		t.Fatalf("%q", got)
	}
}

// Every option must be defined BEFORE the environment is applied, or its
// SSHCHAT_* variable is silently ignored (a flag defined after applyEnv still
// works on the command line but not from a .env).
func TestAllFlagsAreDefinedBeforeTheEnvironmentIsApplied(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	at := strings.Index(text, "applyEnv(flag.CommandLine")
	end := strings.Index(text, "flag.Parse()")
	if at < 0 || end < at {
		t.Fatal("could not find applyEnv / flag.Parse in main.go")
	}
	for _, def := range []string{"flag.String(", "flag.Bool(", "flag.Int(", "flag.Duration(", "flag.IntVar(", "flag.StringVar(", "flag.BoolVar(", "flag.DurationVar("} {
		if strings.Contains(text[at:end], def) {
			t.Errorf("%s appears after applyEnv: that option would ignore its SSHCHAT_* environment variable", def)
		}
	}
}
