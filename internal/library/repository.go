package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/database"
)

// Repository persists tracked titles and chapters.
type Repository struct {
	db *sql.DB
	// MaxDownloadAttempts caps failed retries before a chapter stops
	// counting as missing.
	MaxDownloadAttempts int
}

// CompletedDownload is a persisted completed download record.
type CompletedDownload struct {
	ChapterID  int64
	TitleID    int64
	Label      string
	OutputFile string
}

// NewRepository creates a library repository.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db, MaxDownloadAttempts: DefaultDownloadAttempts}
}

// AddTitle creates or updates a tracked title by source URL.
func (r *Repository) AddTitle(ctx context.Context, params AddTitleParams) (Title, error) {
	params = normalizeTitleParams(params)
	if params.SourceURL == "" {
		return Title{}, errors.New("source URL cannot be empty")
	}
	if _, err := url.ParseRequestURI(params.SourceURL); err != nil {
		return Title{}, fmt.Errorf("invalid source URL: %w", err)
	}
	// The download root is only known at download time; reject traversal
	// segments here so escapes are never even stored.
	if hasDotDot(params.OutputPath) {
		return Title{}, fmt.Errorf("output path %q must not contain ..", params.OutputPath)
	}

	var catalogID any
	if params.CatalogMangaID != nil {
		catalogID = *params.CatalogMangaID
	}
	var sourceID any
	if params.SourceID != "" {
		sourceID = params.SourceID
	}

	row := r.db.QueryRowContext(ctx, `
		INSERT INTO titles (
			catalog_manga_id,
			source_id,
			source_url,
			display_title,
			output_path,
			monitored,
			refresh_interval,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(source_url) DO UPDATE SET
			catalog_manga_id = COALESCE(excluded.catalog_manga_id, titles.catalog_manga_id),
			source_id = COALESCE(excluded.source_id, titles.source_id),
			display_title = excluded.display_title,
			output_path = excluded.output_path,
			monitored = excluded.monitored,
			refresh_interval = excluded.refresh_interval,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id
	`, catalogID, sourceID, params.SourceURL, params.DisplayTitle, params.OutputPath, database.BoolToInt(params.Monitored), params.RefreshInterval)

	var id int64
	if err := row.Scan(&id); err != nil {
		return Title{}, fmt.Errorf("add title: %w", err)
	}

	return r.GetTitle(ctx, id)
}

// GetTitle returns a tracked title by ID.
func (r *Repository) GetTitle(ctx context.Context, id int64) (Title, error) {
	row := r.db.QueryRowContext(ctx, titleSelectQuery()+` WHERE t.id = ? GROUP BY t.id`, id)

	title, err := scanTitle(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Title{}, fmt.Errorf("title %d not found", id)
		}
		return Title{}, fmt.Errorf("get title %d: %w", id, err)
	}

	return title, nil
}

// ListTitles returns all tracked titles with chapter counts.
func (r *Repository) ListTitles(ctx context.Context) ([]Title, error) {
	rows, err := r.db.QueryContext(ctx, titleSelectQuery()+` GROUP BY t.id ORDER BY t.display_title COLLATE NOCASE, t.id`)
	if err != nil {
		return nil, fmt.Errorf("list titles: %w", err)
	}
	defer rows.Close()

	var out []Title
	for rows.Next() {
		title, err := scanTitle(rows)
		if err != nil {
			return nil, fmt.Errorf("scan title: %w", err)
		}
		out = append(out, title)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate titles: %w", err)
	}

	return out, nil
}

// RemoveTitle deletes a tracked title and its dependent chapters/downloads.
func (r *Repository) RemoveTitle(ctx context.Context, id int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM titles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remove title %d: %w", id, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check removed title %d: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("title %d not found", id)
	}

	return nil
}

// LinkSource repoints a title (e.g. an imported one) at a real source URL.
func (r *Repository) LinkSource(ctx context.Context, id int64, sourceURL, sourceID string) error {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return errors.New("source URL cannot be empty")
	}
	var sid any
	if strings.TrimSpace(sourceID) != "" {
		sid = strings.TrimSpace(sourceID)
	}
	var other int64
	switch err := r.db.QueryRowContext(ctx, `SELECT id FROM titles WHERE source_url = ? AND id != ?`, sourceURL, id).Scan(&other); {
	case err == nil:
		return fmt.Errorf("that source is already linked to another tracked title")
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("check existing source link: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `UPDATE titles SET source_url = ?, source_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, sourceURL, sid, id)
	if err != nil {
		return fmt.Errorf("link source for title %d: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check source link %d: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("title %d not found", id)
	}
	return nil
}

// SetMonitored toggles monitoring for a title.
func (r *Repository) SetMonitored(ctx context.Context, id int64, monitored bool) error {
	result, err := r.db.ExecContext(ctx, `UPDATE titles SET monitored = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, database.BoolToInt(monitored), id)
	if err != nil {
		return fmt.Errorf("set monitored %d: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check monitored update %d: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("title %d not found", id)
	}
	return nil
}

// UpsertChapters stores discovered chapters for a title.
func (r *Repository) UpsertChapters(
	ctx context.Context,
	titleID int64,
	discovered []chapters.Chapter,
) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin chapter upsert: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO chapters (
			title_id,
			label,
			title,
			url,
			number_main,
			suffix_type,
			suffix_num,
			updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(title_id, label) DO UPDATE SET
			title = excluded.title,
			url = excluded.url,
			number_main = excluded.number_main,
			suffix_type = excluded.suffix_type,
			suffix_num = excluded.suffix_num,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare chapter upsert: %w", err)
	}
	defer stmt.Close()

	// Chapters also have UNIQUE(title_id, url); a second label for the same
	// URL would abort the whole transaction, so keep the first one.
	seenURL := map[string]bool{}
	count := 0
	for _, ch := range discovered {
		if strings.TrimSpace(ch.Label) == "" || strings.TrimSpace(ch.URL) == "" || seenURL[ch.URL] {
			continue
		}
		seenURL[ch.URL] = true
		if _, err = stmt.ExecContext(
			ctx,
			titleID,
			ch.Label,
			ch.Title,
			ch.URL,
			ch.NumMain,
			ch.SuffixType,
			ch.SuffixNum,
		); err != nil {
			return 0, fmt.Errorf("upsert chapter %q: %w", ch.Label, err)
		}
		count++
	}

	if _, err = tx.ExecContext(ctx, `UPDATE titles SET last_refreshed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, titleID); err != nil {
		return 0, fmt.Errorf("mark title refreshed: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit chapter upsert: %w", err)
	}

	return count, nil
}

// ListMissingChapters returns discovered chapters without a completed
// download, skipping chapters that already failed MaxDownloadAttempts times.
func (r *Repository) ListMissingChapters(ctx context.Context, titleID int64) ([]Chapter, error) {
	rows, err := r.db.QueryContext(ctx, chapterSelectQuery()+`
		LEFT JOIN downloads d
			ON d.chapter_id = c.id
			AND (d.status = 'completed'
				OR (d.status = 'failed' AND d.attempts >= ?))
		WHERE c.title_id = ?
			AND d.id IS NULL
		ORDER BY c.number_main, c.suffix_type, c.suffix_num, c.label
	`, r.MaxDownloadAttempts, titleID)
	if err != nil {
		return nil, fmt.Errorf("list missing chapters: %w", err)
	}
	defer rows.Close()

	var out []Chapter
	for rows.Next() {
		ch, err := scanChapter(rows)
		if err != nil {
			return nil, fmt.Errorf("scan missing chapter: %w", err)
		}
		out = append(out, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate missing chapters: %w", err)
	}

	return out, nil
}

// GetChapterByLabel returns one discovered chapter by title and label.
func (r *Repository) GetChapterByLabel(ctx context.Context, titleID int64, label string) (Chapter, error) {
	row := r.db.QueryRowContext(ctx, chapterSelectQuery()+` WHERE c.title_id = ? AND c.label = ?`, titleID, label)
	chapter, err := scanChapter(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Chapter{}, fmt.Errorf("chapter %q not found", label)
		}
		return Chapter{}, fmt.Errorf("get chapter %q: %w", label, err)
	}

	return chapter, nil
}

// MarkDownloadStarted records that a chapter download started.
func (r *Repository) MarkDownloadStarted(ctx context.Context, chapterID int64) error {
	return r.markDownload(ctx, chapterID, "started", "", 0, "")
}

// MarkDownloadCompleted records that a chapter download completed.
func (r *Repository) MarkDownloadCompleted(ctx context.Context, chapterID int64, outputFile string, bytes int64) error {
	return r.markDownload(ctx, chapterID, "completed", outputFile, bytes, "")
}

// MarkDownloadFailed records that a chapter download failed.
func (r *Repository) MarkDownloadFailed(ctx context.Context, chapterID int64, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	return r.markDownload(ctx, chapterID, "failed", "", 0, msg)
}

// ReconcileStartedDownloads marks interrupted downloads as failed.
func (r *Repository) ReconcileStartedDownloads(ctx context.Context) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE downloads
		SET status = 'failed',
			error = 'download interrupted before completion',
			attempts = attempts + 1,
			completed_at = NULL,
			updated_at = CURRENT_TIMESTAMP
		WHERE status = 'started'
	`)
	if err != nil {
		return 0, fmt.Errorf("reconcile started downloads: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count reconciled downloads: %w", err)
	}
	return count, nil
}

// ListCompletedDownloads returns completed downloads, optionally scoped to a title.
func (r *Repository) ListCompletedDownloads(ctx context.Context, titleID int64) ([]CompletedDownload, error) {
	q := `
		SELECT c.id, c.title_id, c.label, d.output_file
		FROM downloads d
		JOIN chapters c ON c.id = d.chapter_id
		WHERE d.status = 'completed'
	`
	args := []any{}
	if titleID > 0 {
		q += ` AND c.title_id = ?`
		args = append(args, titleID)
	}
	q += ` ORDER BY c.title_id, c.number_main, c.suffix_type, c.suffix_num, c.label`

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list completed downloads: %w", err)
	}
	defer rows.Close()

	var out []CompletedDownload
	for rows.Next() {
		var d CompletedDownload
		if err := rows.Scan(&d.ChapterID, &d.TitleID, &d.Label, &d.OutputFile); err != nil {
			return nil, fmt.Errorf("scan completed download: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate completed downloads: %w", err)
	}

	return out, nil
}

func (r *Repository) markDownload(ctx context.Context, chapterID int64, status, outputFile string, bytes int64, msg string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO downloads (
			chapter_id,
			status,
			output_file,
			bytes,
			attempts,
			error,
			started_at,
			completed_at,
			updated_at
		) VALUES (?, ?, ?, ?, CASE WHEN ? = 'failed' THEN 1 ELSE 0 END, ?, CURRENT_TIMESTAMP, CASE WHEN ? = 'completed' THEN CURRENT_TIMESTAMP END, CURRENT_TIMESTAMP)
		ON CONFLICT(chapter_id) DO UPDATE SET
			status = excluded.status,
			output_file = excluded.output_file,
			bytes = excluded.bytes,
			attempts = CASE excluded.status
				WHEN 'failed' THEN downloads.attempts + 1
				WHEN 'completed' THEN 0
				ELSE downloads.attempts
			END,
			error = excluded.error,
			started_at = CASE WHEN excluded.status = 'started' THEN CURRENT_TIMESTAMP ELSE downloads.started_at END,
			completed_at = CASE WHEN excluded.status = 'completed' THEN CURRENT_TIMESTAMP ELSE NULL END,
			updated_at = CURRENT_TIMESTAMP
	`, chapterID, status, outputFile, bytes, status, msg, status)
	if err != nil {
		return fmt.Errorf("mark download %s for chapter %d: %w", status, chapterID, err)
	}

	return nil
}

func hasDotDot(path string) bool {
	if path == "" {
		return false
	}
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == ".." {
			return true
		}
	}
	return false
}

func normalizeTitleParams(params AddTitleParams) AddTitleParams {
	params.SourceURL = strings.TrimSpace(params.SourceURL)
	params.SourceID = strings.TrimSpace(params.SourceID)
	params.DisplayTitle = strings.TrimSpace(params.DisplayTitle)
	params.OutputPath = strings.TrimSpace(params.OutputPath)
	params.RefreshInterval = strings.TrimSpace(params.RefreshInterval)

	if params.DisplayTitle == "" {
		params.DisplayTitle = params.SourceURL
	}
	if params.RefreshInterval == "" {
		params.RefreshInterval = "24h"
	}

	return params
}

func chapterSelectQuery() string {
	return `
		SELECT
			c.id,
			c.title_id,
			c.label,
			c.title,
			c.url,
			c.number_main,
			c.suffix_type,
			c.suffix_num,
			c.discovered_at,
			c.updated_at
		FROM chapters c
	`
}

func titleSelectQuery() string {
	return `
		SELECT
			t.id,
			t.catalog_manga_id,
			COALESCE(t.source_id, ''),
			t.source_url,
			t.display_title,
			t.output_path,
			t.monitored,
			t.refresh_interval,
			t.last_refreshed_at,
			COUNT(DISTINCT c.id) AS discovered_count,
			COUNT(DISTINCT CASE WHEN d.status = 'completed' THEN d.id END) AS completed_count,
			COUNT(DISTINCT CASE WHEN d.id IS NULL OR d.status != 'completed' THEN c.id END) AS missing_count,
			t.created_at,
			t.updated_at
		FROM titles t
		LEFT JOIN chapters c ON c.title_id = t.id
		LEFT JOIN downloads d ON d.chapter_id = c.id
	`
}

func scanTitle(row database.Scanner) (Title, error) {
	var title Title
	var catalogID sql.NullInt64
	var monitored int
	var lastRefreshed sql.NullString
	var createdAt string
	var updatedAt string

	if err := row.Scan(
		&title.ID,
		&catalogID,
		&title.SourceID,
		&title.SourceURL,
		&title.DisplayTitle,
		&title.OutputPath,
		&monitored,
		&title.RefreshInterval,
		&lastRefreshed,
		&title.DiscoveredCount,
		&title.CompletedCount,
		&title.MissingCount,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Title{}, err
	}

	if catalogID.Valid {
		title.CatalogMangaID = &catalogID.Int64
	}
	title.Monitored = monitored != 0
	if lastRefreshed.Valid {
		t, err := database.ParseTime(lastRefreshed.String)
		if err != nil {
			return Title{}, err
		}
		title.LastRefreshedAt = &t
	}

	created, err := database.ParseTime(createdAt)
	if err != nil {
		return Title{}, err
	}
	updated, err := database.ParseTime(updatedAt)
	if err != nil {
		return Title{}, err
	}
	title.CreatedAt = created
	title.UpdatedAt = updated

	return title, nil
}

func scanChapter(row database.Scanner) (Chapter, error) {
	var chapter Chapter
	var discoveredAt string
	var updatedAt string

	if err := row.Scan(
		&chapter.ID,
		&chapter.TitleID,
		&chapter.Label,
		&chapter.Title,
		&chapter.URL,
		&chapter.NumberMain,
		&chapter.SuffixType,
		&chapter.SuffixNum,
		&discoveredAt,
		&updatedAt,
	); err != nil {
		return Chapter{}, err
	}

	discovered, err := database.ParseTime(discoveredAt)
	if err != nil {
		return Chapter{}, err
	}
	updated, err := database.ParseTime(updatedAt)
	if err != nil {
		return Chapter{}, err
	}
	chapter.DiscoveredAt = discovered
	chapter.UpdatedAt = updated

	return chapter, nil
}
