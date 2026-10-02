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

// keyedFP matches fingerprints of real SSH keys; keyless connections get a
// throwaway "anon-…" identity that must never reserve a nickname or count as
// "last seen".
const keyedFP = "SHA256:%"

// NicknameTaken reports whether a different keyed identity has persisted nick
// (case-insensitive).
func (s *Store) NicknameTaken(ctx context.Context, nick, exceptFP string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identities WHERE nickname = ? COLLATE NOCASE AND fingerprint != ? AND fingerprint LIKE ?`,
		nick, exceptFP, keyedFP).Scan(&n)
	return n > 0, err
}

// TouchIdentity records that fp was just seen (called on disconnect).
func (s *Store) TouchIdentity(ctx context.Context, fp string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE identities SET last_seen_at = ? WHERE fingerprint = ?`, time.Now().Unix(), fp)
	return err
}

// LastSeenByNickname returns when the keyed identity currently using nick was
// last seen.
func (s *Store) LastSeenByNickname(ctx context.Context, nick string) (time.Time, bool, error) {
	var at int64
	err := s.db.QueryRowContext(ctx,
		`SELECT last_seen_at FROM identities WHERE nickname = ? COLLATE NOCASE AND fingerprint LIKE ? ORDER BY last_seen_at DESC LIMIT 1`,
		nick, keyedFP).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return time.Unix(at, 0), true, nil
}
