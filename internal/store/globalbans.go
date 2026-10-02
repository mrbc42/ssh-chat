package store

import (
	"context"
	"time"
)

// GlobalBan is a server-wide ban. A connection matches if its fingerprint
// equals fp (when set) or its IP equals ip (when set).
type GlobalBan struct {
	FP, IP, BannedBy, Reason string
	CreatedAt                time.Time
}

func (s *Store) AddGlobalBan(ctx context.Context, fp, ip, bannedBy, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO global_bans (fp, ip, banned_by, reason, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(fp, ip) DO UPDATE SET banned_by = excluded.banned_by, reason = excluded.reason, created_at = excluded.created_at`,
		fp, ip, bannedBy, reason, time.Now().Unix())
	return err
}

// RemoveGlobalBans deletes every ban whose fingerprint or IP equals key and
// returns how many rows were removed.
func (s *Store) RemoveGlobalBans(ctx context.Context, key string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM global_bans WHERE (fp != '' AND fp = ?) OR (ip != '' AND ip = ?)`, key, key)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) IsGloballyBanned(ctx context.Context, fp, ip string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM global_bans WHERE (fp != '' AND fp = ?) OR (ip != '' AND ip = ?)`, fp, ip).Scan(&n)
	return n > 0, err
}

func (s *Store) ListGlobalBans(ctx context.Context) ([]GlobalBan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT fp, ip, banned_by, reason, created_at FROM global_bans ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GlobalBan
	for rows.Next() {
		var b GlobalBan
		var at int64
		if err := rows.Scan(&b.FP, &b.IP, &b.BannedBy, &b.Reason, &at); err != nil {
			return nil, err
		}
		b.CreatedAt = time.Unix(at, 0)
		out = append(out, b)
	}
	return out, rows.Err()
}
