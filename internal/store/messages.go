package store

import (
	"context"
	"time"
)

type Message struct {
	ID         int64
	ChannelID  int64
	SenderFP   string
	SenderName string
	Body       string
	Kind       string // "msg" | "system"
	CreatedAt  time.Time
}

func (s *Store) AppendMessage(ctx context.Context, m Message) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (channel_id, sender_fp, sender_name, body, kind, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		m.ChannelID, m.SenderFP, m.SenderName, m.Body, m.Kind, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RecentMessages returns up to limit messages for the channel, oldest first.
func (s *Store) RecentMessages(ctx context.Context, channelID int64, limit int) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, channel_id, sender_fp, sender_name, body, kind, created_at
		 FROM messages WHERE channel_id = ? ORDER BY id DESC LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Message
	for rows.Next() {
		var m Message
		var createdAt int64
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.SenderFP, &m.SenderName, &m.Body, &m.Kind, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse to oldest-first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
