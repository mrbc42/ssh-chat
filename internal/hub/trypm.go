package hub

import "time"

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

// ShowMotd sends the message of the day to the user currently named nick, as a
// line of its own kind that the chat window draws as a boxed block. Unlike a
// private message from the bot it is server information, so it is shown even
// to a user who is ignoring the bot. Reports whether it was delivered.
func (h *Hub) ShowMotd(nick, text string) bool {
	to := h.FindSessionByNick(nick)
	if to == nil {
		return false
	}
	line := Line{Time: time.Now(), Kind: KindMotd, Body: text}
	return to.send(Outbound{Line: &line})
}
