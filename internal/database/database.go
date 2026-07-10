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
	if path := os.Getenv("MANGAD_DB"); path != "" {
		return path
	}
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

	// DSN pragmas apply to every pooled connection; executing PRAGMA
	// statements on the *sql.DB would only configure one connection.
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)

	return db, nil
}
