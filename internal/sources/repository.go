package sources

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/brogergvhs/mangad/internal/database"
)

// Repository persists source profiles.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a source repository.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Sync upserts profiles without clearing existing verification status.
// It runs in one transaction so a failure cannot leave a partial sync.
func (r *Repository) Sync(ctx context.Context, profiles []Profile, origin string) (err error) {
	origin = cleanOrigin(origin)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin source sync: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, profile := range profiles {
		p, err := normalizeProfile(profile)
		if err != nil {
			return err
		}
		domains, err := encodeList(p.Domains)
		if err != nil {
			return fmt.Errorf("encode domains for %s: %w", p.ID, err)
		}
		extensions, err := encodeList(p.AllowedExtensions)
		if err != nil {
			return fmt.Errorf("encode extensions for %s: %w", p.ID, err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO sources (
				id,
				origin,
				name,
				domains_json,
				base_url,
				sample_manga_url,
				search_url,
				scraper,
				allowed_extensions_json,
				min_chapters,
				requires_browser_solver,
				requires_browser_downloader,
				enabled,
				profile_version,
				updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(id) DO UPDATE SET
				origin = excluded.origin,
				name = excluded.name,
				domains_json = excluded.domains_json,
				base_url = excluded.base_url,
				sample_manga_url = excluded.sample_manga_url,
				search_url = excluded.search_url,
				scraper = excluded.scraper,
				allowed_extensions_json = excluded.allowed_extensions_json,
				min_chapters = excluded.min_chapters,
				requires_browser_solver = excluded.requires_browser_solver,
				requires_browser_downloader = excluded.requires_browser_downloader,
				enabled = excluded.enabled,
				profile_version = excluded.profile_version,
				updated_at = CURRENT_TIMESTAMP
			WHERE sources.origin != 'local' OR excluded.origin = 'local'
		`, p.ID, origin, p.Name, domains, p.BaseURL, p.SampleMangaURL, p.SearchURL, p.Scraper, extensions, p.MinChapters, database.BoolToInt(p.RequiresBrowserSolver), database.BoolToInt(p.RequiresBrowserDownload), database.BoolToInt(p.Enabled), p.Version)
		if err != nil {
			return fmt.Errorf("sync source %s: %w", p.ID, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit source sync: %w", err)
	}
	return nil
}

// RemoveLocal deletes a local source row.
func (r *Repository) RemoveLocal(ctx context.Context, id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	result, err := r.db.ExecContext(ctx, `DELETE FROM sources WHERE id = ? AND origin = 'local'`, id)
	if err != nil {
		return fmt.Errorf("remove local source %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check removed source %s: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("local source %q not found", id)
	}
	return nil
}

// List returns all known sources.
func (r *Repository) List(ctx context.Context) ([]Source, error) {
	rows, err := r.db.QueryContext(ctx, sourceSelect()+` ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()

	var out []Source
	for rows.Next() {
		src, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sources: %w", err)
	}
	return out, nil
}

// Get returns one source by id.
func (r *Repository) Get(ctx context.Context, id string) (Source, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	row := r.db.QueryRowContext(ctx, sourceSelect()+` WHERE id = ?`, id)
	src, err := scanSource(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Source{}, fmt.Errorf("source %q not found", id)
		}
		return Source{}, fmt.Errorf("get source %q: %w", id, err)
	}
	return src, nil
}

// UpdateCheck stores the latest source verification result.
func (r *Repository) UpdateCheck(ctx context.Context, id, status, lastErr string, chapters, images int, extensions []string, chapterFetch, imageFetch string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	encoded, err := encodeList(extensions)
	if err != nil {
		return fmt.Errorf("encode image extensions: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE sources SET
			status = ?,
			last_checked_at = CURRENT_TIMESTAMP,
			last_error = ?,
			chapters_found = ?,
			sample_images_found = ?,
			image_extensions_json = ?,
			chapter_fetch = CASE WHEN ? = '' THEN chapter_fetch ELSE ? END,
			image_fetch = CASE WHEN ? = '' THEN image_fetch ELSE ? END,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, lastErr, chapters, images, encoded, chapterFetch, chapterFetch, imageFetch, imageFetch, id)
	if err != nil {
		return fmt.Errorf("update source check %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check source update %s: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("source %q not found", id)
	}
	return nil
}

func sourceSelect() string {
	return `
		SELECT
			id,
			origin,
			name,
			domains_json,
			base_url,
			sample_manga_url,
			search_url,
			scraper,
			allowed_extensions_json,
			min_chapters,
			requires_browser_solver,
			requires_browser_downloader,
			enabled,
			profile_version,
			status,
			COALESCE(last_checked_at, ''),
			last_error,
			chapters_found,
			sample_images_found,
			image_extensions_json,
			chapter_fetch,
			image_fetch
		FROM sources`
}

func scanSource(scanner interface {
	Scan(dest ...any) error
}) (Source, error) {
	var src Source
	var domainsJSON, extensionsJSON, imageExtensionsJSON string
	var requiresBrowserSolver, requiresBrowserDownloader, enabled int
	err := scanner.Scan(
		&src.ID,
		&src.Origin,
		&src.Name,
		&domainsJSON,
		&src.BaseURL,
		&src.SampleMangaURL,
		&src.SearchURL,
		&src.Scraper,
		&extensionsJSON,
		&src.MinChapters,
		&requiresBrowserSolver,
		&requiresBrowserDownloader,
		&enabled,
		&src.Version,
		&src.Status,
		&src.LastCheckedAt,
		&src.LastError,
		&src.ChaptersFound,
		&src.SampleImagesFound,
		&imageExtensionsJSON,
		&src.ChapterFetch,
		&src.ImageFetch,
	)
	if err != nil {
		return Source{}, err
	}
	if src.Domains, err = decodeList(domainsJSON); err != nil {
		return Source{}, fmt.Errorf("decode source domains: %w", err)
	}
	if src.AllowedExtensions, err = decodeList(extensionsJSON); err != nil {
		return Source{}, fmt.Errorf("decode source extensions: %w", err)
	}
	if src.ImageExtensions, err = decodeList(imageExtensionsJSON); err != nil {
		return Source{}, fmt.Errorf("decode source image extensions: %w", err)
	}
	src.RequiresBrowserSolver = requiresBrowserSolver != 0
	src.RequiresBrowserDownload = requiresBrowserDownloader != 0
	src.Enabled = enabled != 0
	src.Origin = cleanOrigin(src.Origin)
	return src, nil
}

func cleanOrigin(origin string) string {
	switch origin {
	case OriginBuiltin, OriginRegistry, OriginLocal:
		return origin
	default:
		return OriginLocal
	}
}

func encodeList(values []string) (string, error) {
	data, err := json.Marshal(cleanStrings(values))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func decodeList(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(value), &out); err != nil {
		return nil, err
	}
	return out, nil
}
