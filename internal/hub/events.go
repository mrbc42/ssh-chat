package hub

// Outbound is a message delivered to a single session's UI (via Session.Outbox).
type Outbound struct {
	// Line is a pre-rendered line of text to append to the scrollback (chat,
	// system, error, or help output).
	Line string

	// SwitchRoom, when non-nil, tells the UI to clear its scrollback and
	// switch to displaying this room (used on join/kick/forced-move).
	SwitchRoom *RoomInfo
	// Scrollback is the history replay to show after a SwitchRoom (oldest first).
	Scrollback []string

	// Disconnect, when true, tells the UI to close the session.
	Disconnect bool
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
