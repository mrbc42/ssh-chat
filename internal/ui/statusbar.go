package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/mrbc42/ssh-chat/internal/hub"
)

type statusBarState struct {
	Now         time.Time
	Room        hub.RoomInfo
	TotalOnline int
	StartedAt   time.Time
}

// renderStatusBar builds the single-line status bar, dropping lower-priority
// fields from the right when the terminal is too narrow to fit everything.
func renderStatusBar(st statusBarState, width int, sty *styles) string {
	lock := "unlocked"
	if st.Room.Locked {
		lock = "locked"
	}
	fields := []string{
		st.Now.Format("15:04:05") + "  " + st.Now.Format("2006-01-02"),
		fmt.Sprintf("#%s [%s]", st.Room.Name, lock),
		fmt.Sprintf("%d here", st.Room.Members),
	}
	if st.Room.Topic != "" {
		fields = append(fields, "topic: "+st.Room.Topic)
	}
	fields = append(fields,
		fmt.Sprintf("%d online", st.TotalOnline),
		"up "+formatUptime(time.Since(st.StartedAt)),
	)

	// Drop fields from the end (lowest priority first) until it fits.
	for len(fields) > 1 {
		line := strings.Join(fields, " │ ")
		if len([]rune(line)) <= width-2 || width <= 0 {
			break
		}
		fields = fields[:len(fields)-1]
	}
	line := strings.Join(fields, " │ ")
	return sty.StatusBar.Width(max(width, 0)).Render(line)
}

func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
