package store

import "context"

const schema = `
CREATE TABLE IF NOT EXISTS identities (
    fingerprint   TEXT PRIMARY KEY,
    nickname      TEXT NOT NULL,
    first_seen_at INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS channels (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    topic        TEXT NOT NULL DEFAULT '',
    creator_fp   TEXT NOT NULL,
    locked       INTEGER NOT NULL DEFAULT 0,
    announce     INTEGER NOT NULL DEFAULT 1,
    created_at   INTEGER NOT NULL,
    is_main      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS messages (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id    INTEGER NOT NULL REFERENCES channels(id),
    sender_fp     TEXT NOT NULL,
    sender_name   TEXT NOT NULL,
    body          TEXT NOT NULL,
    kind          TEXT NOT NULL DEFAULT 'msg',
    created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_channel_time ON messages(channel_id, id DESC);

CREATE TABLE IF NOT EXISTS operators (
    channel_id  INTEGER NOT NULL REFERENCES channels(id),
    fp          TEXT NOT NULL,
    role        TEXT NOT NULL,
    granted_at  INTEGER NOT NULL,
    PRIMARY KEY (channel_id, fp)
);

CREATE TABLE IF NOT EXISTS bans (
    channel_id  INTEGER NOT NULL REFERENCES channels(id),
    fp          TEXT NOT NULL DEFAULT '',
    ip          TEXT NOT NULL DEFAULT '',
    banned_by   TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (channel_id, fp, ip)
);

CREATE TABLE IF NOT EXISTS admin_alerts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    reporter_fp   TEXT NOT NULL,
    reporter_nick TEXT NOT NULL,
    channel_name  TEXT NOT NULL,
    message       TEXT NOT NULL,
    delivered     INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);
`

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}
