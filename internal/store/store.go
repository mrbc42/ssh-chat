// Package store provides SQLite-backed persistence for channels, messages,
// identities, bans, operators, and admin alerts.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

type Store struct {
	db  *sql.DB // writes and everything low-rate (one connection: SQLite has one writer)
	rdb *sql.DB // read-only connections for the hot "recent history" query

	// Chat messages are persisted by a background writer, in batches, so the
	// rooms never wait for the disk: with a synchronous INSERT per message,
	// profiling a 1000-user run showed every room blocked on the single
	// shared connection.
	qmu    sync.RWMutex
	closed bool
	msgq   chan writeReq
	wdone  chan struct{}
}

type writeReq struct {
	m       Message
	barrier chan struct{} // non-nil: closed once everything queued before it is committed
}

const dsnPragmas = "_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)"

func Open(path string) (*Store, error) {
	// synchronous=NORMAL in WAL mode: no fsync per commit. A power cut can lose
	// the last moments of chat history but cannot corrupt the database.
	db, err := sql.Open("sqlite", path+"?"+dsnPragmas+"&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite only tolerates one writer at a time; serialize via a single
	// connection so concurrent room actors don't hit SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, msgq: make(chan writeReq, 16384), wdone: make(chan struct{})}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	rdb, err := sql.Open("sqlite", path+"?"+dsnPragmas+"&_pragma=query_only(1)")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open sqlite (read): %w", err)
	}
	rdb.SetMaxOpenConns(4)
	s.rdb = rdb
	go s.runWriter()
	return s, nil
}

// Close flushes queued messages, then closes the database.
func (s *Store) Close() error {
	s.qmu.Lock()
	if !s.closed {
		s.closed = true
		close(s.msgq)
	}
	s.qmu.Unlock()
	<-s.wdone
	_ = s.rdb.Close()
	return s.db.Close()
}
