package store

import (
	"context"
	"time"
)

// An invite lets one identity (by SSH key fingerprint) enter a channel while it
// is locked. Owners, operators and server admins never need one.

func (s *Store) AddInvite(ctx context.Context, channelID int64, fp, invitedBy string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (channel_id, fp, invited_by, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(channel_id, fp) DO UPDATE SET invited_by = excluded.invited_by`,
		channelID, fp, invitedBy, time.Now().Unix())
	return err
}

// RemoveInvite revokes an invite and reports whether there was one.
func (s *Store) RemoveInvite(ctx context.Context, channelID int64, fp string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE channel_id = ? AND fp = ?`, channelID, fp)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) IsInvited(ctx context.Context, channelID int64, fp string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invites WHERE channel_id = ? AND fp = ?`, channelID, fp).Scan(&n)
	return n > 0, err
}

// ListInvites returns the fingerprints invited to a channel, oldest first.
func (s *Store) ListInvites(ctx context.Context, channelID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fp FROM invites WHERE channel_id = ? ORDER BY created_at, fp`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, err
		}
		out = append(out, fp)
	}
	return out, rows.Err()
}
