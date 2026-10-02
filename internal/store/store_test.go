package store

import (
	"context"
	"path/filepath"
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
