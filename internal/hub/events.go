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
	KindAdmin  LineKind = "admin"  // delivered admin alert
)

// Line is one line of chat/system/command output, carrying enough
// structure for the UI to render it in color without the hub package
// needing to know about styling.
type Line struct {
	Time   time.Time
	Kind   LineKind
	Sender string // nickname; set only for KindChat
	Body   string
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

	// Disconnect, when true, tells the UI to close the session.
	Disconnect bool
}

func chatLine(sender, body string) Line {
	return Line{Time: time.Now(), Kind: KindChat, Sender: sender, Body: body}
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
type evPart struct{ sess *Session }
type evChat struct {
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

func (evJoin) isRoomEvent()        {}
func (evPart) isRoomEvent()        {}
func (evChat) isRoomEvent()        {}
func (evSystem) isRoomEvent()      {}
func (evForceRemove) isRoomEvent() {}
func (evSetLocked) isRoomEvent()   {}
func (evSetAnnounce) isRoomEvent() {}
func (evSetTopic) isRoomEvent()    {}
func (evWho) isRoomEvent()         {}
func (evSnapshot) isRoomEvent()    {}
func (evFindMember) isRoomEvent()  {}
func (evFindAdmins) isRoomEvent()  {}
