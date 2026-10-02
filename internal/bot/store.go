package bot

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// store is the bot's own SQLite database (separate from the chat DB so the
// chat schema is untouched).
type botStore struct{ db *sql.DB }

const botSchema = `
CREATE TABLE IF NOT EXISTS users (
    fp              TEXT PRIMARY KEY,
    first_seen      INTEGER NOT NULL,
    last_seen       INTEGER NOT NULL,
    visits          INTEGER NOT NULL DEFAULT 0,
    messages        INTEGER NOT NULL DEFAULT 0,
    last_nick       TEXT NOT NULL,
    anniv_announced INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_users_nick ON users(last_nick COLLATE NOCASE);
CREATE TABLE IF NOT EXISTS optout (fp TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS tells (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    to_fp      TEXT NOT NULL,
    from_fp    TEXT NOT NULL,
    from_nick  TEXT NOT NULL,
    body       TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tells_to ON tells(to_fp);
CREATE TABLE IF NOT EXISTS scores (
    fp      TEXT PRIMARY KEY,
    nick    TEXT NOT NULL,
    points  INTEGER NOT NULL DEFAULT 0,
    correct INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT NOT NULL);
`

func openStore(path string) (*botStore, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(botSchema); err != nil {
		db.Close()
		return nil, err
	}
	return &botStore{db: db}, nil
}

func (s *botStore) close() error { return s.db.Close() }

var ctxbg = context.Background()

type userRow struct {
	FP        string
	FirstSeen time.Time
	LastSeen  time.Time
	Visits    int
	Messages  int
	LastNick  string
	Anniv     int
}

func (s *botStore) user(fp string) (userRow, bool) {
	var u userRow
	var f, l int64
	err := s.db.QueryRowContext(ctxbg,
		`SELECT fp, first_seen, last_seen, visits, messages, last_nick, anniv_announced FROM users WHERE fp = ?`, fp).
		Scan(&u.FP, &f, &l, &u.Visits, &u.Messages, &u.LastNick, &u.Anniv)
	if err != nil {
		return userRow{}, false
	}
	u.FirstSeen, u.LastSeen = time.Unix(f, 0), time.Unix(l, 0)
	return u, true
}

// recordVisit upserts the user for a new login and returns the row as it was
// BEFORE this visit (ok=false if brand new) plus the updated row.
func (s *botStore) recordVisit(fp, nick string, now time.Time) (prev userRow, existed bool, cur userRow) {
	prev, existed = s.user(fp)
	if existed {
		_, _ = s.db.ExecContext(ctxbg, `UPDATE users SET visits = visits + 1, last_seen = ?, last_nick = ? WHERE fp = ?`, now.Unix(), nick, fp)
	} else {
		_, _ = s.db.ExecContext(ctxbg, `INSERT INTO users (fp, first_seen, last_seen, visits, last_nick) VALUES (?, ?, ?, 1, ?)`, fp, now.Unix(), now.Unix(), nick)
	}
	cur, _ = s.user(fp)
	return prev, existed, cur
}

func (s *botStore) touchLastSeen(fp string, now time.Time) {
	_, _ = s.db.ExecContext(ctxbg, `UPDATE users SET last_seen = ? WHERE fp = ?`, now.Unix(), fp)
}

func (s *botStore) setNick(fp, nick string) {
	_, _ = s.db.ExecContext(ctxbg, `UPDATE users SET last_nick = ? WHERE fp = ?`, nick, fp)
}

// bumpMessages increments the user's message count and returns the new count.
func (s *botStore) bumpMessages(fp string) int {
	_, _ = s.db.ExecContext(ctxbg, `UPDATE users SET messages = messages + 1 WHERE fp = ?`, fp)
	var n int
	_ = s.db.QueryRowContext(ctxbg, `SELECT messages FROM users WHERE fp = ?`, fp).Scan(&n)
	return n
}

func (s *botStore) setAnniv(fp string, years int) {
	_, _ = s.db.ExecContext(ctxbg, `UPDATE users SET anniv_announced = ? WHERE fp = ?`, years, fp)
}

// userByNick finds the most recently seen tracked user with that nick.
func (s *botStore) userByNick(nick string) (userRow, bool) {
	var fp string
	err := s.db.QueryRowContext(ctxbg,
		`SELECT fp FROM users WHERE last_nick = ? COLLATE NOCASE ORDER BY last_seen DESC LIMIT 1`, nick).Scan(&fp)
	if err != nil {
		return userRow{}, false
	}
	return s.user(fp)
}

func (s *botStore) countUsers() int {
	var n int
	_ = s.db.QueryRowContext(ctxbg, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n
}

// ---- opt-out ----

func (s *botStore) isOptedOut(fp string) bool {
	var n int
	_ = s.db.QueryRowContext(ctxbg, `SELECT COUNT(*) FROM optout WHERE fp = ?`, fp).Scan(&n)
	return n > 0
}

// forget erases everything held about fp and records the opt-out.
func (s *botStore) forget(fp string) {
	for _, q := range []string{
		`DELETE FROM users WHERE fp = ?`,
		`DELETE FROM tells WHERE to_fp = ? OR from_fp = ?`,
		`DELETE FROM scores WHERE fp = ?`,
		`INSERT OR IGNORE INTO optout (fp) VALUES (?)`,
	} {
		if q == `DELETE FROM tells WHERE to_fp = ? OR from_fp = ?` {
			_, _ = s.db.ExecContext(ctxbg, q, fp, fp)
		} else {
			_, _ = s.db.ExecContext(ctxbg, q, fp)
		}
	}
}

func (s *botStore) remember(fp string) {
	_, _ = s.db.ExecContext(ctxbg, `DELETE FROM optout WHERE fp = ?`, fp)
}

// ---- tells ----

type tellRow struct {
	ID       int64
	FromNick string
	Body     string
	At       time.Time
}

func (s *botStore) addTell(toFP, fromFP, fromNick, body string, now time.Time) error {
	_, err := s.db.ExecContext(ctxbg,
		`INSERT INTO tells (to_fp, from_fp, from_nick, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		toFP, fromFP, fromNick, body, now.Unix())
	return err
}

func (s *botStore) countTellsTo(fp string) int {
	var n int
	_ = s.db.QueryRowContext(ctxbg, `SELECT COUNT(*) FROM tells WHERE to_fp = ?`, fp).Scan(&n)
	return n
}

func (s *botStore) countTellsFrom(fp string) int {
	var n int
	_ = s.db.QueryRowContext(ctxbg, `SELECT COUNT(*) FROM tells WHERE from_fp = ?`, fp).Scan(&n)
	return n
}

// takeTells returns and deletes every pending tell for fp, oldest first.
func (s *botStore) takeTells(fp string) []tellRow {
	rows, err := s.db.QueryContext(ctxbg, `SELECT id, from_nick, body, created_at FROM tells WHERE to_fp = ? ORDER BY id`, fp)
	if err != nil {
		return nil
	}
	var out []tellRow
	for rows.Next() {
		var t tellRow
		var at int64
		if rows.Scan(&t.ID, &t.FromNick, &t.Body, &at) == nil {
			t.At = time.Unix(at, 0)
			out = append(out, t)
		}
	}
	rows.Close()
	for _, t := range out {
		_, _ = s.db.ExecContext(ctxbg, `DELETE FROM tells WHERE id = ?`, t.ID)
	}
	return out
}

func (s *botStore) purgeTells(olderThan time.Time) {
	_, _ = s.db.ExecContext(ctxbg, `DELETE FROM tells WHERE created_at < ?`, olderThan.Unix())
}

// ---- trivia scores ----

func (s *botStore) addScore(fp, nick string, points int) {
	_, _ = s.db.ExecContext(ctxbg,
		`INSERT INTO scores (fp, nick, points, correct) VALUES (?, ?, ?, 1)
		 ON CONFLICT(fp) DO UPDATE SET points = points + excluded.points, correct = correct + 1, nick = excluded.nick`,
		fp, nick, points)
}

type scoreRow struct {
	Nick    string
	Points  int
	Correct int
}

func (s *botStore) topScores(n int) []scoreRow {
	rows, err := s.db.QueryContext(ctxbg, `SELECT nick, points, correct FROM scores ORDER BY points DESC, correct DESC, nick LIMIT ?`, n)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []scoreRow
	for rows.Next() {
		var r scoreRow
		if rows.Scan(&r.Nick, &r.Points, &r.Correct) == nil {
			out = append(out, r)
		}
	}
	return out
}

// ---- key/value state ----

func (s *botStore) get(k string) string {
	var v string
	if err := s.db.QueryRowContext(ctxbg, `SELECT v FROM kv WHERE k = ?`, k).Scan(&v); errors.Is(err, sql.ErrNoRows) || err != nil {
		return ""
	}
	return v
}

func (s *botStore) set(k, v string) {
	_, _ = s.db.ExecContext(ctxbg, `INSERT INTO kv (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v`, k, v)
}

func (s *botStore) getInt(k string) int {
	n, _ := strconv.Atoi(s.get(k))
	return n
}

func (s *botStore) incr(k string) int {
	n := s.getInt(k) + 1
	s.set(k, strconv.Itoa(n))
	return n
}
