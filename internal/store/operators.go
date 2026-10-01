package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	RoleOwner    = "owner"
	RoleOperator = "operator"
)

// IsOwnerOrOp returns the fp's role in channelID ("" if none) and whether
// they have at least operator-level permission.
func (s *Store) IsOwnerOrOp(ctx context.Context, channelID int64, fp string) (role string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT role FROM operators WHERE channel_id = ? AND fp = ?`, channelID, fp).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return role, true, nil
}

func (s *Store) SetOperator(ctx context.Context, channelID int64, fp, role string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO operators (channel_id, fp, role, granted_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(channel_id, fp) DO UPDATE SET role = excluded.role`,
		channelID, fp, role, now)
	return err
}

func (s *Store) RemoveOperator(ctx context.Context, channelID int64, fp string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM operators WHERE channel_id = ? AND fp = ? AND role != ?`,
		channelID, fp, RoleOwner)
	return err
}
