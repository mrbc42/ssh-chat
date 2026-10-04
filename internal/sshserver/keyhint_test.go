package sshserver

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrbc42/ssh-chat/internal/hub"
	"github.com/mrbc42/ssh-chat/internal/store"
)

// The "No SSH key detected" notice ends with the command for the guide, but
// only when the bot that answers it is running.
func TestKeyNoticePointsAtTheGuideOnlyWhenTheBotIsRunning(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "k.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := hub.NewHub(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := keyGuideHint(h); got != "" {
		t.Fatalf("no bot, no hint: %q", got)
	}
	h.SetBotHelp([]string{"x"})
	if got := keyGuideHint(h); !strings.HasSuffix(strings.TrimSpace(got), "!ssh for a step-by-step guide.") {
		t.Fatalf("with the bot running the notice should end with the command: %q", got)
	}
}
