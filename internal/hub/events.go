package hub

import "time"

// LineKind tags a Line so the UI can style it distinctly (nick color for
// chat, red for errors, etc.) without the hub package knowing anything
// about rendering.
type LineKind string

const (
	KindChat   LineKind = "chat"   // a user's chat message (Sender set)
	KindSystem LineKind = "system" // join/leave/kick/ban/lock/topic/op notices
	KindError  LineKind = "error"  // command error / permission denial
	KindInfo   LineKind = "info"   // help/list/who output, confirmations
	KindAdmin  LineKind = "admin"  // delivered admin alert / server broadcast
	KindPM     LineKind = "pm"     // private message (Sender + Dir set)
	KindAction LineKind = "action" // /me emote (Sender set)
)

// Line is one line of chat/system/command output, carrying enough
// structure for the UI to render it in color without the hub package
// needing to know about styling.
type Line struct {
	Time   time.Time
	Kind   LineKind
	Sender string // nickname; set for KindChat and KindPM
	Body   string
	Dir    string // KindPM only: "to" or "from" (the other party named in Sender)
}

// Outbound is a message delivered to a single session's UI (via Session.Outbox).
type Outbound struct {
	// Line, when non-nil, is a line to append to the scrollback.
	Line *Line

	// SwitchRoom, when non-nil, tells the UI to clear its scrollback and
	// switch to displaying this room (used on join/kick/forced-move).
	SwitchRoom *RoomInfo
	// Scrollback is the history replay to show after a SwitchRoom (oldest first).
	Scrollback []Line

	// Clear, when true, tells the UI to empty its scrollback pane.
	Clear bool

	// Disconnect, when true, tells the UI to close the session.
	Disconnect bool
}

func chatLine(sender, body string) Line {
	return Line{Time: time.Now(), Kind: KindChat, Sender: sender, Body: body}
}

func actionLine(sender, body string) Line {
	return Line{Time: time.Now(), Kind: KindAction, Sender: sender, Body: body}
}

func systemLine(body string) Line {
	return Line{Time: time.Now(), Kind: KindSystem, Body: body}
}

func infoLine(body string) Line {
	return Line{Time: time.Now(), Kind: KindInfo, Body: body}
}

func errorLine(body string) Line {
	return Line{Time: time.Now(), Kind: KindError, Body: body}
}

func adminLine(body string) Line {
	return Line{Time: time.Now(), Kind: KindAdmin, Body: body}
}

func pmLine(otherNick, dir, body string) Line {
	return Line{Time: time.Now(), Kind: KindPM, Sender: otherNick, Dir: dir, Body: body}
}

// RoomInfo is a snapshot of a room's public state for status bar / UI use.
type RoomInfo struct {
	Name     string
	Topic    string
	Locked   bool
	Announce bool
	Members  int
}

// roomEvent is the internal message type processed by a Room's actor loop.
type roomEvent interface{ isRoomEvent() }

type evJoin struct{ sess *Session }

// partReason distinguishes why a session is leaving a room, so the
// announcement wording matches what actually happened.
type partReason string

const (
	partReasonLeave      partReason = ""           // plain /leave or being replaced into another room
	partReasonSwitch     partReason = "switch"     // left #main specifically to join another channel
	partReasonDisconnect partReason = "disconnect" // the whole SSH session ended
)

type evPart struct {
	sess   *Session
	reason partReason
}
type evChat struct {
	sess *Session
	body string
}
type evReap struct{}
type evSessions struct{ respond chan []*Session }
type evShutdown struct{ done chan struct{} }
type evAction struct {
	sess *Session
	body string
}
type evSystem struct {
	text    string
	persist bool
}
type evForceRemove struct {
	target        *Session
	broadcastText string // shown to remaining room members
	targetText    string // shown to the removed session itself
}
type evSetLocked struct {
	locked bool
	by     *Session
}
type evSetAnnounce struct {
	announce bool
	by       *Session
}
type evSetTopic struct {
	topic string
	by    *Session
}
type evWho struct{ respond chan []string }
type evSnapshot struct{ respond chan RoomInfo }
type evFindMember struct {
	nick    string
	respond chan []*Session
}
type evFindAdmins struct {
	adminFPs map[string]bool
	respond  chan []*Session
}
type evAdminAnnounce struct{ text string }

func (evJoin) isRoomEvent()          {}
func (evPart) isRoomEvent()          {}
func (evChat) isRoomEvent()          {}
func (evReap) isRoomEvent()          {}
func (evSessions) isRoomEvent()      {}
func (evShutdown) isRoomEvent()      {}
func (evAction) isRoomEvent()        {}
func (evSystem) isRoomEvent()        {}
func (evForceRemove) isRoomEvent()   {}
func (evSetLocked) isRoomEvent()     {}
func (evSetAnnounce) isRoomEvent()   {}
func (evSetTopic) isRoomEvent()      {}
func (evWho) isRoomEvent()           {}
func (evSnapshot) isRoomEvent()      {}
func (evFindMember) isRoomEvent()    {}
func (evFindAdmins) isRoomEvent()    {}
func (evAdminAnnounce) isRoomEvent() {}
