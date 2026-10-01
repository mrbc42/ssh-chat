package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const MainChannelName = "main"

var ErrChannelExists = errors.New("channel already exists")
var ErrChannelNotFound = errors.New("channel not found")

type Channel struct {
	ID        int64
	Name      string
	Topic     string
	CreatorFP string
	Locked    bool
	Announce  bool
	CreatedAt time.Time
	IsMain    bool
}

func scanChannel(row interface{ Scan(...any) error }) (Channel, error) {
	var c Channel
	var locked, announce, isMain int
	var createdAt int64
	err := row.Scan(&c.ID, &c.Name, &c.Topic, &c.CreatorFP, &locked, &announce, &createdAt, &isMain)
	if err != nil {
		return Channel{}, err
	}
	c.Locked = locked != 0
	c.Announce = announce != 0
	c.IsMain = isMain != 0
	c.CreatedAt = time.Unix(createdAt, 0)
	return c, nil
}

const channelCols = "id, name, topic, creator_fp, locked, announce, created_at, is_main"

func (s *Store) GetOrCreateMainChannel(ctx context.Context) (Channel, error) {
	ch, ok, err := s.GetChannelByName(ctx, MainChannelName)
	if err != nil {
		return Channel{}, err
	}
	if ok {
		return ch, nil
	}
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO channels (name, topic, creator_fp, locked, announce, created_at, is_main)
		 VALUES (?, '', 'system', 0, 1, ?, 1)`, MainChannelName, now)
	if err != nil {
		return Channel{}, err
	}
	ch, _, err = s.GetChannelByName(ctx, MainChannelName)
	return ch, err
}

func (s *Store) CreateChannel(ctx context.Context, name, creatorFP string) (Channel, error) {
	_, ok, err := s.GetChannelByName(ctx, name)
	if err != nil {
		return Channel{}, err
	}
	if ok {
		return Channel{}, ErrChannelExists
	}
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO channels (name, topic, creator_fp, locked, announce, created_at, is_main)
		 VALUES (?, '', ?, 0, 1, ?, 0)`, name, creatorFP, now)
	if err != nil {
		return Channel{}, err
	}
	ch, _, err := s.GetChannelByName(ctx, name)
	if err != nil {
		return Channel{}, err
	}
	if err := s.SetOperator(ctx, ch.ID, creatorFP, "owner"); err != nil {
		return Channel{}, err
	}
	return ch, nil
}

func (s *Store) GetChannelByName(ctx context.Context, name string) (Channel, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE name = ? COLLATE NOCASE`, name)
	ch, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, false, nil
	}
	if err != nil {
		return Channel{}, false, err
	}
	return ch, true, nil
}

func (s *Store) GetChannelByID(ctx context.Context, id int64) (Channel, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE id = ?`, id)
	ch, err := scanChannel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, false, nil
	}
	if err != nil {
		return Channel{}, false, err
	}
	return ch, true, nil
}

func (s *Store) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+channelCols+` FROM channels ORDER BY is_main DESC, name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

func (s *Store) SetLocked(ctx context.Context, channelID int64, locked bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channels SET locked = ? WHERE id = ?`, boolToInt(locked), channelID)
	return err
}

func (s *Store) SetAnnounce(ctx context.Context, channelID int64, announce bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channels SET announce = ? WHERE id = ?`, boolToInt(announce), channelID)
	return err
}

func (s *Store) SetTopic(ctx context.Context, channelID int64, topic string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE channels SET topic = ? WHERE id = ?`, topic, channelID)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
