package ui

import (
	"sync"

	"github.com/muesli/termenv"

	"github.com/mrbc42/ssh-chat/internal/hub"
)

// Every user who hops into a channel re-renders the same recent history, and
// every chat message is styled once per recipient, identically: same text,
// same width, same colour profile. Profiling at 1000 users showed that
// repeated styling was ~60% of the server's CPU. So rendered rows are cached
// and shared by all sessions, keyed by everything that affects the output.

type rowKey struct {
	kind         hub.LineKind
	sender, body string
	dir          string
	minute       int64 // the timestamp shows HH:MM only
	width        int
	profile      termenv.Profile
}

const rowCacheMax = 50000

var rowCache = struct {
	sync.RWMutex
	m map[rowKey][]string
}{m: make(map[rowKey][]string)}

// cachedRows is renderRows through the shared cache. The returned slice must
// be treated as read-only (callers only copy its elements).
func cachedRows(l hub.Line, sty *styles, width int) []string {
	k := rowKey{l.Kind, l.Sender, l.Body, l.Dir, l.Time.Unix() / 60, width, sty.profile}
	rowCache.RLock()
	rows, ok := rowCache.m[k]
	rowCache.RUnlock()
	if ok {
		return rows
	}
	rows = renderRows(l, sty, width)
	rowCache.Lock()
	if len(rowCache.m) >= rowCacheMax {
		rowCache.m = make(map[rowKey][]string) // simple bound; refills quickly
	}
	rowCache.m[k] = rows
	rowCache.Unlock()
	return rows
}
