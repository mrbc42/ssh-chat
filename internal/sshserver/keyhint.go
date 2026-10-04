package sshserver

import "github.com/mrbc42/ssh-chat/internal/hub"

// keyGuideHint ends the "No SSH key detected" notice with the command that
// shows the step-by-step guide, when the bot that provides it is running.
func keyGuideHint(h *hub.Hub) string {
	if h.BotRunning() {
		return " Type !ssh for a step-by-step guide."
	}
	return ""
}
