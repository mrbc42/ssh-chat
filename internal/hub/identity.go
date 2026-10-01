package hub

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// RandomAnonFP generates a per-connection anonymous identity for clients
// that offer no SSH public key. It is never persisted/reused across
// reconnects.
func RandomAnonFP() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "anon-" + hex.EncodeToString(b)
}

// DefaultNickFor derives a default display name from a fingerprint, e.g.
// "guest-7f3a1c9e" -> "guest-7f3a". Used the first time an identity is seen.
func DefaultNickFor(fp string) string {
	h := fp
	if len(h) > 8 {
		h = h[len(h)-8:]
	}
	return fmt.Sprintf("guest-%s", h)
}
