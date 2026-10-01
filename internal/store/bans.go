package store

import (
	"context"
	"time"
)

func (s *Store) AddBan(ctx context.Context, channelID int64, fp, ip, bannedBy, reason string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO bans (channel_id, fp, ip, banned_by, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id, fp, ip) DO UPDATE SET banned_by = excluded.banned_by, reason = excluded.reason, created_at = excluded.created_at`,
		channelID, fp, ip, bannedBy, reason, now)
	return err
}

func (s *Store) RemoveBan(ctx context.Context, channelID int64, fp string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM bans WHERE channel_id = ? AND fp = ?`, channelID, fp)
	return err
}

func (s *Store) IsBanned(ctx context.Context, channelID int64, fp, ip string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bans WHERE channel_id = ? AND ((fp != '' AND fp = ?) OR (ip != '' AND ip = ?))`,
		channelID, fp, ip).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
