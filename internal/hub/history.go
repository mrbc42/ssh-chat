package hub

import (
	"context"
	"math"
)

const (
	defaultHistory = 100
	maxHistory     = 500
)

// History returns up to n messages of the channel sess is in, older than the
// message with id beforeID (math.MaxInt64 = from the newest), oldest first,
// without the lines of users sess is ignoring. exhausted is true when the
// channel has nothing older than what was returned.
func (h *Hub) History(ctx context.Context, sess *Session, beforeID int64, n int) (lines []Line, exhausted bool, err error) {
	if n <= 0 {
		n = defaultHistory
	}
	if n > maxHistory {
		n = maxHistory
	}
	if beforeID <= 0 {
		beforeID = math.MaxInt64
	}
	room := sess.CurrentRoom()
	if room == nil {
		room = h.Main()
	}
	msgs, err := h.store.MessagesBefore(ctx, room.ID, beforeID, n)
	if err != nil {
		return nil, false, err
	}
	for _, m := range msgs {
		if l := lineFromMessage(m); !sess.ignoresLine(l) {
			lines = append(lines, l)
		}
	}
	return lines, len(msgs) < n, nil
}
