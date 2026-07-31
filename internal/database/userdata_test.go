package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestBackupRestoreUserDataSkipsDownloadsAndRuntimeState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	srcPath := filepath.Join(t.TempDir(), "src.db")
	src := openMigrated(t, ctx, srcPath)
	seedUserData(t, ctx, src)
	src.Close()

	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := BackupUserData(ctx, srcPath, backup); err != nil {
		t.Fatalf("BackupUserData() error = %v", err)
	}

	bak := openRaw(t, ctx, backup)
	assertCount(t, ctx, bak, "settings", 1)
	assertCount(t, ctx, bak, "chapter_read_progress", 1)
	assertCount(t, ctx, bak, "downloads", 0)
	assertCount(t, ctx, bak, "jobs", 0)
	assertCount(t, ctx, bak, "sessions", 0)
	bak.Close()

	dstPath := filepath.Join(t.TempDir(), "dst.db")
	dst := openMigrated(t, ctx, dstPath)
	if _, err := dst.ExecContext(ctx, `INSERT INTO jobs(type, status, payload_json) VALUES ('refresh_title', 'queued', '{}')`); err != nil {
		t.Fatal(err)
	}
	dst.Close()

	if err := RestoreUserData(ctx, dstPath, backup); err != nil {
		t.Fatalf("RestoreUserData() error = %v", err)
	}

	dst = openRaw(t, ctx, dstPath)
	assertCount(t, ctx, dst, "settings", 1)
	assertCount(t, ctx, dst, "users", 1)
	assertCount(t, ctx, dst, "chapters", 1)
	assertCount(t, ctx, dst, "chapter_read_progress", 1)
	assertCount(t, ctx, dst, "downloads", 0)
	assertCount(t, ctx, dst, "jobs", 0)
	dst.Close()
}

func openMigrated(t *testing.T, ctx context.Context, path string) *sql.DB {
	t.Helper()
	db := openRaw(t, ctx, path)
	if err := Migrate(ctx, db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func openRaw(t *testing.T, ctx context.Context, path string) *sql.DB {
	t.Helper()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func seedUserData(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO roles(id, name, origin, permissions_json) VALUES (1, 'owner', 'local', '[]')`,
		`INSERT INTO users(id, username, password_hash, role_id, origin) VALUES (1, 'u', 'hash', 1, 'local')`,
		`INSERT INTO settings(key, value) VALUES ('serve.run_every', '5s')`,
		`INSERT INTO catalog_manga(id, provider, provider_id, title_romaji) VALUES (1, 'anilist', '1', 'Demo')`,
		`INSERT INTO titles(id, catalog_manga_id, source_url, display_title) VALUES (1, 1, 'https://example.test/manga', 'Demo')`,
		`INSERT INTO chapters(id, title_id, label, url) VALUES (1, 1, '1', 'https://example.test/chapter/1')`,
		`INSERT INTO chapter_read_progress(user_id, chapter_id, last_page, read_pages, total_pages) VALUES (1, 1, 4, 5, 10)`,
		`INSERT INTO downloads(chapter_id, status, output_file) VALUES (1, 'completed', '/downloads/demo.cbz')`,
		`INSERT INTO jobs(type, status, payload_json) VALUES ('download_missing', 'queued', '{}')`,
		`INSERT INTO sessions(token_hash, user_id, expires_at) VALUES ('tok', 1, '2999-01-01T00:00:00Z')`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

func assertCount(t *testing.T, ctx context.Context, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+quoteIdent(table)).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}
