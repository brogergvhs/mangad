package database

import (
	"context"
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
		{"title_source_matches", "chapters_found"},
		{"title_source_matches", "updated_at"},
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
