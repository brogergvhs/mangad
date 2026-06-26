// Package database opens and migrates the local MangaD SQLite database.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/brogergvhs/mangad/internal/config"

	_ "modernc.org/sqlite"
)

// DefaultPath returns the default application database path.
func DefaultPath() string {
	return filepath.Join(config.ConfigRoot(), "mangad.db")
}

// Open opens a SQLite database and applies connection-level pragmas.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite wal mode: %w", err)
	}

	return db, nil
}
