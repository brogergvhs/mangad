package database

import (
	"context"
	"database/sql"
	"fmt"
)

const initialSchemaVersion = 1

const sourcesSchema = `
CREATE TABLE IF NOT EXISTS sources (
	id TEXT PRIMARY KEY,
	origin TEXT NOT NULL DEFAULT 'local',
	name TEXT NOT NULL,
	domains_json TEXT NOT NULL DEFAULT '[]',
	base_url TEXT NOT NULL DEFAULT '',
	sample_manga_url TEXT NOT NULL DEFAULT '',
	search_url TEXT NOT NULL DEFAULT '',
	scraper TEXT NOT NULL DEFAULT 'generic',
	allowed_extensions_json TEXT NOT NULL DEFAULT '[]',
	min_chapters INTEGER NOT NULL DEFAULT 0,
	requires_browser_solver INTEGER NOT NULL DEFAULT 0,
	requires_browser_downloader INTEGER NOT NULL DEFAULT 0,
	single_manga INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1,
	profile_version TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'unknown',
	last_checked_at TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	chapters_found INTEGER NOT NULL DEFAULT 0,
	sample_images_found INTEGER NOT NULL DEFAULT 0,
	image_extensions_json TEXT NOT NULL DEFAULT '[]',
	chapter_fetch TEXT NOT NULL DEFAULT '',
	image_fetch TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

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
	volumes INTEGER,
	synonyms_json TEXT NOT NULL DEFAULT '[]',
	genres_json TEXT NOT NULL DEFAULT '[]',
	authors_json TEXT NOT NULL DEFAULT '[]',
	year INTEGER NOT NULL DEFAULT 0,
	average_score INTEGER NOT NULL DEFAULT 0,
	wanted INTEGER NOT NULL DEFAULT 0,
	raw_json TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(provider, provider_id)
);
` + sourcesSchema + `
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
	title TEXT NOT NULL DEFAULT '',
	confidence REAL NOT NULL DEFAULT 0,
	match_method TEXT NOT NULL DEFAULT 'manual',
	chapters_found INTEGER NOT NULL DEFAULT 0,
	sample_images_found INTEGER NOT NULL DEFAULT 0,
	error TEXT NOT NULL DEFAULT '',
	verified_at TEXT,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
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
	attempts INTEGER NOT NULL DEFAULT 0,
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

CREATE TABLE IF NOT EXISTS browser_cookies (
	domain TEXT NOT NULL,
	path TEXT NOT NULL DEFAULT '/',
	name TEXT NOT NULL,
	value TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	secure INTEGER NOT NULL DEFAULT 0,
	http_only INTEGER NOT NULL DEFAULT 0,
	user_agent TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY(domain, path, name)
);

CREATE INDEX IF NOT EXISTS idx_titles_monitored ON titles(monitored);
CREATE INDEX IF NOT EXISTS idx_chapters_title_id ON chapters(title_id);
CREATE INDEX IF NOT EXISTS idx_downloads_chapter_id ON downloads(chapter_id);
CREATE INDEX IF NOT EXISTS idx_jobs_status_run_after ON jobs(status, run_after);
CREATE INDEX IF NOT EXISTS idx_browser_cookies_expires_at ON browser_cookies(expires_at);

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

	if err = dropObsoleteSources(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, initialSchema); err != nil {
		return fmt.Errorf("apply migration %d: %w", initialSchemaVersion, err)
	}
	if err = ensureColumn(ctx, tx, "sources", "requires_browser_downloader", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate sources.requires_browser_downloader: %w", err)
	}
	if err = ensureColumn(ctx, tx, "sources", "search_url", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("migrate sources.search_url: %w", err)
	}
	if err = ensureColumn(ctx, tx, "sources", "single_manga", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return fmt.Errorf("migrate sources.single_manga: %w", err)
	}
	for _, col := range []string{"chapter_fetch", "image_fetch"} {
		if err = ensureColumn(ctx, tx, "sources", col, "TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("migrate sources.%s: %w", col, err)
		}
	}
	for _, col := range []struct{ table, name, def string }{
		{"catalog_manga", "synonyms_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"catalog_manga", "wanted", "INTEGER NOT NULL DEFAULT 0"},
		{"catalog_manga", "volumes", "INTEGER"},
		{"catalog_manga", "genres_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"catalog_manga", "authors_json", "TEXT NOT NULL DEFAULT '[]'"},
		{"catalog_manga", "year", "INTEGER NOT NULL DEFAULT 0"},
		{"catalog_manga", "average_score", "INTEGER NOT NULL DEFAULT 0"},
		{"downloads", "attempts", "INTEGER NOT NULL DEFAULT 0"},
		{"title_source_matches", "title", "TEXT NOT NULL DEFAULT ''"},
		{"title_source_matches", "chapters_found", "INTEGER NOT NULL DEFAULT 0"},
		{"title_source_matches", "sample_images_found", "INTEGER NOT NULL DEFAULT 0"},
		{"title_source_matches", "error", "TEXT NOT NULL DEFAULT ''"},
		{"title_source_matches", "updated_at", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err = ensureColumn(ctx, tx, col.table, col.name, col.def); err != nil {
			return fmt.Errorf("migrate %s.%s: %w", col.table, col.name, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}

	return nil
}

// dropObsoleteSources removes the pre-origin sources table so initialSchema
// can recreate it; ON DELETE SET NULL clears references from titles.
func dropObsoleteSources(ctx context.Context, tx *sql.Tx) error {
	ok, exists, err := txHasColumn(ctx, tx, "sources", "origin")
	if err != nil || !exists || ok {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE sources`); err != nil {
		return fmt.Errorf("drop obsolete sources table: %w", err)
	}
	return nil
}

func ensureColumn(ctx context.Context, tx *sql.Tx, table, column, def string) error {
	ok, _, err := txHasColumn(ctx, tx, table, column)
	if err != nil || ok {
		return err
	}
	_, err = tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def))
	return err
}

func txHasColumn(ctx context.Context, tx *sql.Tx, table, column string) (bool, bool, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
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
