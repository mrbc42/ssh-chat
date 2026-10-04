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

// SetOwner makes fp the one owner of the channel: any other owner is demoted to
// operator (so the previous owner keeps their moderation rights, just not the
// ownership).
func (s *Store) SetOwner(ctx context.Context, channelID int64, fp string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE operators SET role = ? WHERE channel_id = ? AND role = ? AND fp != ?`,
		RoleOperator, channelID, RoleOwner, fp); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO operators (channel_id, fp, role, granted_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(channel_id, fp) DO UPDATE SET role = excluded.role`,
		channelID, fp, RoleOwner, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Operator is one row of a channel's staff list.
type Operator struct {
	FP        string
	Role      string // RoleOwner or RoleOperator
	GrantedAt time.Time
}

// ListOperators returns a channel's owner(s) first, then its operators, oldest
// grant first within each.
func (s *Store) ListOperators(ctx context.Context, channelID int64) ([]Operator, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT fp, role, granted_at FROM operators WHERE channel_id = ?
		 ORDER BY CASE role WHEN ? THEN 0 ELSE 1 END, granted_at, fp`, channelID, RoleOwner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Operator
	for rows.Next() {
		var o Operator
		var at int64
		if err := rows.Scan(&o.FP, &o.Role, &at); err != nil {
			return nil, err
		}
		o.GrantedAt = time.Unix(at, 0)
		out = append(out, o)
	}
	return out, rows.Err()
}
