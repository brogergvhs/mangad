package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/brogergvhs/mangad/internal/chapters"
)

// Repository persists tracked titles and chapters.
type Repository struct {
	db *sql.DB
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
	return &Repository{db: db}
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
			source_id = excluded.source_id,
			display_title = excluded.display_title,
			output_path = excluded.output_path,
			monitored = excluded.monitored,
			refresh_interval = excluded.refresh_interval,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id
	`, catalogID, sourceID, params.SourceURL, params.DisplayTitle, params.OutputPath, boolToInt(params.Monitored), params.RefreshInterval)

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

	for _, ch := range discovered {
		if strings.TrimSpace(ch.Label) == "" || strings.TrimSpace(ch.URL) == "" {
			continue
		}
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
	}

	if _, err = tx.ExecContext(ctx, `UPDATE titles SET last_refreshed_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, titleID); err != nil {
		return 0, fmt.Errorf("mark title refreshed: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit chapter upsert: %w", err)
	}

	return len(discovered), nil
}

// ListMissingChapters returns discovered chapters without a completed download.
func (r *Repository) ListMissingChapters(ctx context.Context, titleID int64) ([]Chapter, error) {
	rows, err := r.db.QueryContext(ctx, chapterSelectQuery()+`
		LEFT JOIN downloads d
			ON d.chapter_id = c.id
			AND d.status = 'completed'
		WHERE c.title_id = ?
			AND d.id IS NULL
		ORDER BY c.number_main, c.suffix_type, c.suffix_num, c.label
	`, titleID)
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
			error,
			started_at,
			completed_at,
			updated_at
		) VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CASE WHEN ? = 'completed' THEN CURRENT_TIMESTAMP END, CURRENT_TIMESTAMP)
		ON CONFLICT(chapter_id) DO UPDATE SET
			status = excluded.status,
			output_file = excluded.output_file,
			bytes = excluded.bytes,
			error = excluded.error,
			started_at = CASE WHEN excluded.status = 'started' THEN CURRENT_TIMESTAMP ELSE downloads.started_at END,
			completed_at = CASE WHEN excluded.status = 'completed' THEN CURRENT_TIMESTAMP ELSE NULL END,
			updated_at = CURRENT_TIMESTAMP
	`, chapterID, status, outputFile, bytes, msg, status)
	if err != nil {
		return fmt.Errorf("mark download %s for chapter %d: %w", status, chapterID, err)
	}

	return nil
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

type scanner interface {
	Scan(dest ...any) error
}

func scanTitle(row scanner) (Title, error) {
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
		t, err := parseTime(lastRefreshed.String)
		if err != nil {
			return Title{}, err
		}
		title.LastRefreshedAt = &t
	}

	created, err := parseTime(createdAt)
	if err != nil {
		return Title{}, err
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return Title{}, err
	}
	title.CreatedAt = created
	title.UpdatedAt = updated

	return title, nil
}

func scanChapter(row scanner) (Chapter, error) {
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

	discovered, err := parseTime(discoveredAt)
	if err != nil {
		return Chapter{}, err
	}
	updated, err := parseTime(updatedAt)
	if err != nil {
		return Chapter{}, err
	}
	chapter.DiscoveredAt = discovered
	chapter.UpdatedAt = updated

	return chapter, nil
}

func parseTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("parse sqlite time %q", value)
}

func boolToInt(value bool) int {
	if value {
		return 1
	}

	return 0
}
