package hub

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
)

// RandomAnonFP generates a per-connection anonymous identity for clients
// that offer no SSH public key. It is never persisted/reused across
// reconnects.
func RandomAnonFP() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "anon-" + hex.EncodeToString(b)
}

// adjectives and nouns used to build a mashup default nickname, e.g.
// "cosmic-badger", in the style of Docker/Gitea's auto-generated names —
// friendlier than a raw hex fingerprint suffix while still being
// deterministic per identity (same fingerprint always gets the same
// default until the user picks their own with /nick).
var adjectives = []string{
	"cosmic", "rusty", "nimble", "feral", "lucky", "shadow", "velvet", "rogue",
	"frosty", "gilded", "wild", "quiet", "stormy", "sly", "bold", "hollow",
	"amber", "crimson", "jade", "silver", "golden", "midnight", "electric",
	"ancient", "lazy", "grumpy", "spry", "plucky", "cheeky", "vivid",
}

var nouns = []string{
	"badger", "falcon", "otter", "raven", "wombat", "lynx", "viper", "heron",
	"jackal", "panther", "ferret", "gecko", "weasel", "hyena", "mantis",
	"cobra", "puffin", "walrus", "koala", "mongoose", "pelican", "yak",
	"stoat", "vulture", "toad", "newt", "marmot", "ocelot", "shrike", "tapir",
}

// DefaultNickFor derives a mashup default display name from a fingerprint,
// e.g. "cosmic-badger". Deterministic per fingerprint, used the first time
// an identity is seen; the user can change it anytime with /nick.
func DefaultNickFor(fp string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(fp))
	sum := h.Sum32()
	adj := adjectives[sum%uint32(len(adjectives))]
	noun := nouns[(sum/uint32(len(adjectives)))%uint32(len(nouns))]
	return fmt.Sprintf("%s-%s", adj, noun)
}
