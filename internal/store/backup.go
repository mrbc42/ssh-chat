package store

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Backup writes a consistent, compacted copy of the database to dest using
// VACUUM INTO (safe while the server is running; dest must not exist).
func (s *Store) Backup(ctx context.Context, dest string) error {
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("backup destination %s already exists", dest)
	}
	_, err := s.db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(dest, "'", "''")+"'")
	return err
}
