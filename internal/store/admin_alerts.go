package store

import (
	"context"
	"time"
)

type AdminAlert struct {
	ID           int64
	ReporterFP   string
	ReporterNick string
	ChannelName  string
	Message      string
	Delivered    bool
	CreatedAt    time.Time
}

func (s *Store) AddAdminAlert(ctx context.Context, a AdminAlert) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_alerts (reporter_fp, reporter_nick, channel_name, message, delivered, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		a.ReporterFP, a.ReporterNick, a.ChannelName, a.Message, boolToInt(a.Delivered), now)
	return err
}

func (s *Store) RecentAdminAlerts(ctx context.Context, limit int) ([]AdminAlert, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, reporter_fp, reporter_nick, channel_name, message, delivered, created_at
		 FROM admin_alerts ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AdminAlert
	for rows.Next() {
		var a AdminAlert
		var delivered int
		var createdAt int64
		if err := rows.Scan(&a.ID, &a.ReporterFP, &a.ReporterNick, &a.ChannelName, &a.Message, &delivered, &createdAt); err != nil {
			return nil, err
		}
		a.Delivered = delivered != 0
		a.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}
