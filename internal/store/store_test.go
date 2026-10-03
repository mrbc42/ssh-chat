package store

import (
	"context"
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
