package hub

import "crypto/rand"

// randRange returns a uniformly distributed integer in [min, max].
func randRange(min, max int) int {
	span := int64(max - min + 1)
	b := make([]byte, 1)
	for {
		if _, err := rand.Read(b); err != nil {
			continue
		}
		// Reject values that would bias the distribution via modulo.
		if int64(b[0]) >= (256/span)*span {
			continue
		}
		return min + int(int64(b[0])%span)
	}
}
