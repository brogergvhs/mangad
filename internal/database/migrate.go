package database

import (
	"context"
	"database/sql"
	"fmt"
)

const initialSchemaVersion = 1

const initialSchema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS catalog_manga (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	provider TEXT NOT NULL,
	provider_id TEXT NOT NULL,
	title_romaji TEXT NOT NULL DEFAULT '',
	title_english TEXT NOT NULL DEFAULT '',
	title_native TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	cover_image TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	format TEXT NOT NULL DEFAULT '',
	chapters INTEGER,
	raw_json TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(provider, provider_id)
);

CREATE TABLE IF NOT EXISTS sources (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	domains_json TEXT NOT NULL DEFAULT '[]',
	status TEXT NOT NULL DEFAULT 'unknown',
	last_checked_at TEXT,
	last_error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS titles (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	catalog_manga_id INTEGER REFERENCES catalog_manga(id) ON DELETE SET NULL,
	source_id TEXT REFERENCES sources(id) ON DELETE SET NULL,
	source_url TEXT NOT NULL UNIQUE,
	display_title TEXT NOT NULL,
	output_path TEXT NOT NULL DEFAULT '',
	monitored INTEGER NOT NULL DEFAULT 1,
	refresh_interval TEXT NOT NULL DEFAULT '24h',
	last_refreshed_at TEXT,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS title_source_matches (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	catalog_manga_id INTEGER NOT NULL REFERENCES catalog_manga(id) ON DELETE CASCADE,
	source_id TEXT REFERENCES sources(id) ON DELETE SET NULL,
	source_url TEXT NOT NULL,
	confidence REAL NOT NULL DEFAULT 0,
	match_method TEXT NOT NULL DEFAULT 'manual',
	verified_at TEXT,
	UNIQUE(catalog_manga_id, source_url)
);

CREATE TABLE IF NOT EXISTS chapters (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	title_id INTEGER NOT NULL REFERENCES titles(id) ON DELETE CASCADE,
	label TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	url TEXT NOT NULL,
	number_main INTEGER NOT NULL DEFAULT 0,
	suffix_type TEXT NOT NULL DEFAULT '',
	suffix_num INTEGER NOT NULL DEFAULT 0,
	discovered_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(title_id, label),
	UNIQUE(title_id, url)
);

CREATE TABLE IF NOT EXISTS downloads (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	chapter_id INTEGER NOT NULL REFERENCES chapters(id) ON DELETE CASCADE,
	status TEXT NOT NULL,
	output_file TEXT NOT NULL DEFAULT '',
	bytes INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	started_at TEXT,
	completed_at TEXT,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(chapter_id)
);

CREATE TABLE IF NOT EXISTS jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	status TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '{}',
	run_after TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	attempts INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_titles_monitored ON titles(monitored);
CREATE INDEX IF NOT EXISTS idx_chapters_title_id ON chapters(title_id);
CREATE INDEX IF NOT EXISTS idx_downloads_chapter_id ON downloads(chapter_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status_run_after ON jobs(status, run_after);

INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);
`

// Migrate applies all built-in database migrations.
func Migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, initialSchema); err != nil {
		return fmt.Errorf("apply migration %d: %w", initialSchemaVersion, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}

	return nil
}
