package database

import (
	"context"
	"path/filepath"
	"testing"
)

// A second Migrate run must not re-clobber genuine (manual=0) progress whose
// pages share a single timestamp — the backfill is one-shot.
func TestManualBackfillRunsOnce(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "kaodoku.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO titles (source_url, display_title) VALUES ('u','T')`)
	mustExec(`INSERT INTO chapters (id, title_id, label, url) VALUES (1,1,'1','c')`)
	mustExec(`INSERT INTO chapter_read_progress (user_id, chapter_id, completed, manual) VALUES (1,1,1,0)`)
	mustExec(`INSERT INTO chapter_read_pages (user_id, chapter_id, page, read_at) VALUES (1,1,1,'2026-01-01 10:00:00')`)

	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var manual int
	if err := db.QueryRowContext(ctx, `SELECT manual FROM chapter_read_progress WHERE chapter_id = 1`).Scan(&manual); err != nil {
		t.Fatal(err)
	}
	if manual != 0 {
		t.Fatalf("manual = %d after re-migrate, want 0 (backfill reran)", manual)
	}
}
