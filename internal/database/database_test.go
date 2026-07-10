package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenAndMigrate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if maxOpen := db.Stats().MaxOpenConnections; maxOpen != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", maxOpen)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 1`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations error = %v", err)
	}
	if count != 1 {
		t.Fatalf("schema migration count = %d, want 1", count)
	}
	ok, exists, err := tableHasColumn(ctx, db, "sources", "requires_browser_downloader")
	if err != nil {
		t.Fatalf("tableHasColumn() error = %v", err)
	}
	if !exists || !ok {
		t.Fatalf("sources.requires_browser_downloader exists=%t ok=%t", exists, ok)
	}
	for _, tc := range []struct {
		table  string
		column string
	}{
		{"catalog_manga", "wanted"},
		{"catalog_manga", "synonyms_json"},
		{"sources", "search_url"},
		{"title_source_matches", "chapters_found"},
		{"title_source_matches", "updated_at"},
		{"chapter_read_progress", "read_pages"},
		{"chapter_read_pages", "read_at"},
	} {
		ok, exists, err := tableHasColumn(ctx, db, tc.table, tc.column)
		if err != nil {
			t.Fatalf("tableHasColumn(%s.%s) error = %v", tc.table, tc.column, err)
		}
		if !exists || !ok {
			t.Fatalf("%s.%s exists=%t ok=%t", tc.table, tc.column, exists, ok)
		}
	}
}

func TestOpenAppliesPragmasToEveryConnection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)

	// Hold two connections at once so both are checked.
	conn1, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()
	conn2, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()

	for i, conn := range []*sql.Conn{conn1, conn2} {
		var enabled int
		if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enabled); err != nil {
			t.Fatalf("conn %d: query foreign_keys: %v", i, err)
		}
		if enabled != 1 {
			t.Errorf("conn %d: foreign_keys = %d, want 1", i, enabled)
		}
		var timeout int
		if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
			t.Fatalf("conn %d: query busy_timeout: %v", i, err)
		}
		if timeout != 5000 {
			t.Errorf("conn %d: busy_timeout = %d, want 5000", i, timeout)
		}
	}
}

func TestMigrateRecreatesObsoleteSourcesTable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE sources (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			domains_json TEXT NOT NULL DEFAULT '[]',
			status TEXT NOT NULL DEFAULT 'unknown',
			last_checked_at TEXT,
			last_error TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		t.Fatalf("create old sources table error = %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	ok, exists, err := tableHasColumn(ctx, db, "sources", "origin")
	if err != nil {
		t.Fatalf("tableHasColumn() error = %v", err)
	}
	if !exists || !ok {
		t.Fatalf("sources.origin exists=%t ok=%t", exists, ok)
	}
}

// tableHasColumn reports whether table exists and whether it has column.
func tableHasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, false, err
	}
	defer rows.Close()

	var exists bool
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, false, err
		}
		exists = true
		if name == column {
			return true, true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, false, err
	}
	return false, exists, nil
}
