package store

import (
	"context"
	"database/sql"
	"log"
	"time"
)

type Message struct {
	ID         int64
	ChannelID  int64
	SenderFP   string
	SenderName string
	Body       string
	Kind       string // "msg" | "system"
	CreatedAt  time.Time
}

// AppendMessageAsync queues m for the background writer and returns at once.
// The message keeps the time it was queued. After Close it is dropped.
func (s *Store) AppendMessageAsync(m Message) {
	m.CreatedAt = time.Now()
	s.qmu.RLock()
	defer s.qmu.RUnlock()
	if s.closed {
		return
	}
	s.msgq <- writeReq{m: m} // blocks only if 16k messages are already waiting
}

// Flush returns once every message queued before the call has been committed.
func (s *Store) Flush() {
	b := make(chan struct{})
	s.qmu.RLock()
	if s.closed {
		s.qmu.RUnlock()
		return
	}
	s.msgq <- writeReq{barrier: b}
	s.qmu.RUnlock()
	<-b
}

// runWriter commits queued messages in batches. While a commit is in flight
// the queue grows, so the next batch is larger: group commit for free.
func (s *Store) runWriter() {
	defer close(s.wdone)
	batch := make([]writeReq, 0, 512)
	for req := range s.msgq {
		batch = append(batch[:0], req)
	gather:
		for len(batch) < 512 {
			select {
			case more, ok := <-s.msgq:
				if !ok {
					break gather
				}
				batch = append(batch, more)
			default:
				break gather
			}
		}
		s.commit(batch)
	}
}

func (s *Store) commit(batch []writeReq) {
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err == nil {
		var stmt *sql.Stmt
		if stmt, err = tx.PrepareContext(ctx,
			`INSERT INTO messages (channel_id, sender_fp, sender_name, body, kind, created_at) VALUES (?, ?, ?, ?, ?, ?)`); err == nil {
			for _, r := range batch {
				if r.barrier != nil {
					continue
				}
				if _, err = stmt.ExecContext(ctx, r.m.ChannelID, r.m.SenderFP, r.m.SenderName, r.m.Body, r.m.Kind, r.m.CreatedAt.Unix()); err != nil {
					break
				}
			}
			stmt.Close()
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	if err != nil {
		log.Printf("store: dropping %d queued messages: %v", len(batch), err)
	}
	for _, r := range batch {
		if r.barrier != nil {
			close(r.barrier)
		}
	}
}

func (s *Store) AppendMessage(ctx context.Context, m Message) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (channel_id, sender_fp, sender_name, body, kind, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		m.ChannelID, m.SenderFP, m.SenderName, m.Body, m.Kind, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RecentMessages returns up to limit messages for the channel, oldest first.
func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var createdAt int64
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.SenderFP, &m.SenderName, &m.Body, &m.Kind, &createdAt); err != nil {
			return nil, err
		}
		m.CreatedAt = time.Unix(createdAt, 0)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// the queries return newest first; callers want oldest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *Store) RecentMessages(ctx context.Context, channelID int64, limit int) ([]Message, error) {
	rows, err := s.rdb.QueryContext(ctx,
		`SELECT id, channel_id, sender_fp, sender_name, body, kind, created_at
		 FROM messages WHERE channel_id = ? ORDER BY id DESC LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// MessagesBefore returns up to limit messages of a channel older than the
// message with id beforeID, oldest first (for paging back through history).
func (s *Store) MessagesBefore(ctx context.Context, channelID, beforeID int64, limit int) ([]Message, error) {
	rows, err := s.rdb.QueryContext(ctx,
		`SELECT id, channel_id, sender_fp, sender_name, body, kind, created_at
		 FROM messages WHERE channel_id = ? AND id < ? ORDER BY id DESC LIMIT ?`, channelID, beforeID, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// PruneMessages deletes messages older than cutoff and returns how many went.
// It works in small chunks through the shared write connection so that live
// chat writes interleave instead of waiting behind one huge DELETE.
func (s *Store) PruneMessages(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM messages WHERE id IN (SELECT id FROM messages WHERE created_at < ? LIMIT 2000)`, cutoff.Unix())
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n == 0 {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
