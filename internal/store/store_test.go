package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestBackupIsOpenable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNickname(ctx, "SHA256:x", "nick"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "backup.db")
	if err := st.Backup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	if err := st.Backup(ctx, dest); err == nil {
		t.Fatal("overwriting an existing backup must fail")
	}
	b, err := Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	if nick, ok, _ := b.ResolveNickname(ctx, "SHA256:x"); !ok || nick != "nick" {
		t.Fatalf("backup missing data: %q %v", nick, ok)
	}
}

func TestAsyncWriterKeepsOrderAndFlushIsABarrier(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	ch, err := st.GetOrCreateMainChannel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const n = 3000 // more than one batch
	for i := 0; i < n; i++ {
		st.AppendMessageAsync(Message{ChannelID: ch.ID, SenderFP: "fp", SenderName: "bob", Body: "msg " + strconv.Itoa(i), Kind: "msg"})
	}
	st.Flush()
	got, err := st.RecentMessages(ctx, ch.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 || got[0].Body != "msg "+strconv.Itoa(n-50) || got[49].Body != "msg "+strconv.Itoa(n-1) {
		t.Fatalf("after Flush the last 50 must be visible, oldest first: first=%q last=%q len=%d", got[0].Body, got[len(got)-1].Body, len(got))
	}
}

func TestCloseDrainsTheQueue(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "d.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := st.GetOrCreateMainChannel(ctx)
	for i := 0; i < 2000; i++ {
		st.AppendMessageAsync(Message{ChannelID: ch.ID, SenderFP: "fp", SenderName: "bob", Body: "m", Kind: "msg"})
	}
	if err := st.Close(); err != nil { // no Flush: Close itself must drain
		t.Fatal(err)
	}
	st.AppendMessageAsync(Message{ChannelID: ch.ID, Body: "after close"}) // must not panic or block
	st.Flush()

	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	got, _ := again.RecentMessages(ctx, ch.ID, 5000)
	if len(got) != 2000 {
		t.Fatalf("Close lost queued messages: %d of 2000 persisted", len(got))
	}
}

func TestSynchronousNormalAndReadPool(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var mode int
	if err := st.db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&mode); err != nil || mode != 1 {
		t.Fatalf("synchronous = %d (want 1 = NORMAL), err %v", mode, err)
	}
	if _, err := st.rdb.ExecContext(ctx, `INSERT INTO kv_does_not_matter VALUES (1)`); err == nil {
		t.Fatal("the read pool must be read-only")
	}
}

func TestMessagesBeforePagesBackwards(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "b.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ch, _ := st.GetOrCreateMainChannel(ctx)
	other, _ := st.CreateChannel(ctx, "other", "fp")
	for i := 1; i <= 30; i++ {
		cid := ch.ID
		if i%3 == 0 {
			cid = other.ID // interleaved ids from another channel
		}
		st.AppendMessage(ctx, Message{ChannelID: cid, SenderFP: "fp", SenderName: "bob", Body: "m" + strconv.Itoa(i), Kind: "msg"})
	}
	newest, _ := st.RecentMessages(ctx, ch.ID, 5)
	page, err := st.MessagesBefore(ctx, ch.ID, newest[0].ID, 5)
	if err != nil || len(page) != 5 {
		t.Fatalf("page: %d err %v", len(page), err)
	}
	for i := 1; i < len(page); i++ {
		if page[i].ID <= page[i-1].ID {
			t.Fatal("page must be oldest first")
		}
	}
	if page[len(page)-1].ID >= newest[0].ID {
		t.Fatal("page must be strictly older than the cursor")
	}
	for _, m := range page {
		if m.ChannelID != ch.ID {
			t.Fatal("page leaked another channel")
		}
	}
	all, _ := st.MessagesBefore(ctx, ch.ID, 1<<62, 1000)
	if len(all) != 20 {
		t.Fatalf("whole channel = %d, want 20", len(all))
	}
}

func TestPruneMessagesDeletesOnlyOldOnes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ch, _ := st.GetOrCreateMainChannel(ctx)
	now := time.Now()
	old, fresh := now.AddDate(0, 0, -120).Unix(), now.AddDate(0, 0, -10).Unix()
	tx, _ := st.db.Begin()
	for i := 0; i < 4500; i++ { // more than two delete chunks
		tx.Exec(`INSERT INTO messages (channel_id, sender_fp, sender_name, body, kind, created_at) VALUES (?, 'fp', 'bob', 'old', 'msg', ?)`, ch.ID, old)
	}
	for i := 0; i < 25; i++ {
		tx.Exec(`INSERT INTO messages (channel_id, sender_fp, sender_name, body, kind, created_at) VALUES (?, 'fp', 'bob', 'fresh', 'msg', ?)`, ch.ID, fresh)
	}
	tx.Commit()

	n, err := st.PruneMessages(ctx, now.AddDate(0, 0, -90))
	if err != nil || n != 4500 {
		t.Fatalf("pruned %d (err %v), want 4500", n, err)
	}
	left, _ := st.RecentMessages(ctx, ch.ID, 10000)
	if len(left) != 25 || left[0].Body != "fresh" {
		t.Fatalf("%d left, want the 25 fresh ones", len(left))
	}
	if n, _ := st.PruneMessages(ctx, now.AddDate(0, 0, -90)); n != 0 {
		t.Fatalf("second prune deleted %d", n)
	}
	var idx int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'idx_messages_created'`).Scan(&idx); err != nil || idx != 1 {
		t.Fatal("created_at index missing: pruning would scan the whole table")
	}
}

// A database created before entry messages existed is upgraded in place, keeps
// its data, and the new column then works.
func TestOldDatabaseGainsTheEntryMessageColumn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE COLLATE NOCASE,
		topic TEXT NOT NULL DEFAULT '', creator_fp TEXT NOT NULL, locked INTEGER NOT NULL DEFAULT 0,
		announce INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, is_main INTEGER NOT NULL DEFAULT 0);
		INSERT INTO channels (name, topic, creator_fp, created_at, is_main) VALUES ('main', 'old topic', 'fp', 1, 1), ('lounge', '', 'fp', 2, 0);`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	st, err := Open(path) // migrates
	if err != nil {
		t.Fatalf("opening an old database: %v", err)
	}
	ch, ok, err := st.GetChannelByName(ctx, "lounge")
	if err != nil || !ok || ch.EntryMessage != "" {
		t.Fatalf("old channel unreadable after upgrade: %+v ok=%v err=%v", ch, ok, err)
	}
	if main, _, _ := st.GetChannelByName(ctx, "main"); main.Topic != "old topic" {
		t.Fatal("existing data was lost in the upgrade")
	}
	if err := st.SetEntryMessage(ctx, ch.ID, "Welcome, friend"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	again, err := Open(path) // a second open must not try to add the column again
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer again.Close()
	if ch2, _, _ := again.GetChannelByName(ctx, "lounge"); ch2.EntryMessage != "Welcome, friend" {
		t.Fatalf("entry message not persisted: %q", ch2.EntryMessage)
	}
}

func TestInvites(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, _ := st.CreateChannel(ctx, "alpha", "SHA256:o")
	b, _ := st.CreateChannel(ctx, "beta", "SHA256:o")

	if ok, _ := st.IsInvited(ctx, a.ID, "SHA256:x"); ok {
		t.Fatal("nobody is invited by default")
	}
	if err := st.AddInvite(ctx, a.ID, "SHA256:x", "SHA256:o"); err != nil {
		t.Fatal(err)
	}
	_ = st.AddInvite(ctx, a.ID, "SHA256:x", "SHA256:o") // idempotent
	_ = st.AddInvite(ctx, a.ID, "SHA256:y", "SHA256:o")
	if ok, _ := st.IsInvited(ctx, a.ID, "SHA256:x"); !ok {
		t.Fatal("x should be invited to alpha")
	}
	if ok, _ := st.IsInvited(ctx, b.ID, "SHA256:x"); ok {
		t.Fatal("an invite is per channel")
	}
	if l, _ := st.ListInvites(ctx, a.ID); len(l) != 2 || l[0] != "SHA256:x" {
		t.Fatalf("list = %v", l)
	}
	if removed, _ := st.RemoveInvite(ctx, a.ID, "SHA256:x"); !removed {
		t.Fatal("remove should report it existed")
	}
	if removed, _ := st.RemoveInvite(ctx, a.ID, "SHA256:x"); removed {
		t.Fatal("removing twice should report nothing to remove")
	}
	// Deleting the channel deletes its invites.
	if err := st.DeleteChannel(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := st.ListInvites(ctx, a.ID); len(l) != 0 {
		t.Fatalf("invites survived the channel: %v", l)
	}
}
