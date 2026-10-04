package hub

// TryPM sends a private message from one session to the user currently named
// toNick, exactly as /msg does, but reports whether it was really delivered:
// false if the user is offline, is ignoring the sender, or their screen queue
// is full. The bot uses this so that mail is only deleted once it has arrived.
func (h *Hub) TryPM(from *Session, toNick, text string) bool {
	to := h.FindSessionByNick(toNick)
	if to == nil || to.isIgnoring(from.Nick()) {
		return false
	}
	if len(text) > maxMessageLen {
		text = text[:maxMessageLen]
	}
	text = h.mask(text)
	in := pmLine(from.Nick(), "from", text)
	if !to.send(Outbound{Line: &in}) {
		return false
	}
	out := pmLine(to.Nick(), "to", text)
	from.send(Outbound{Line: &out})
	return true
}
