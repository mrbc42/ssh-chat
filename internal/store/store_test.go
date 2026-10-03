package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
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
