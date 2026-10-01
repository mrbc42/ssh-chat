package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ResolveNickname returns the stored nickname for fp, and whether it was known.
func (s *Store) ResolveNickname(ctx context.Context, fp string) (string, bool, error) {
	var nick string
	err := s.db.QueryRowContext(ctx, `SELECT nickname FROM identities WHERE fingerprint = ?`, fp).Scan(&nick)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	now := time.Now().Unix()
	_, _ = s.db.ExecContext(ctx, `UPDATE identities SET last_seen_at = ? WHERE fingerprint = ?`, now, fp)
	return nick, true, nil
}

func (s *Store) SetNickname(ctx context.Context, fp, nick string) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO identities (fingerprint, nickname, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(fingerprint) DO UPDATE SET nickname = excluded.nickname, last_seen_at = excluded.last_seen_at`,
		fp, nick, now, now)
	return err
}

// FindFingerprintByNickname looks up the most recently seen identity using a
// given nickname (case-insensitive). Used to resolve ban/kick targets that
// may have disconnected already.
func (s *Store) FindFingerprintByNickname(ctx context.Context, nick string) (string, bool, error) {
	var fp string
	err := s.db.QueryRowContext(ctx,
		`SELECT fingerprint FROM identities WHERE nickname = ? COLLATE NOCASE ORDER BY last_seen_at DESC LIMIT 1`, nick).Scan(&fp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return fp, true, nil
}
