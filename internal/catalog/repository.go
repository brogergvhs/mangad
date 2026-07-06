package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/brogergvhs/mangad/internal/database"
)

// Repository persists canonical manga and source matches.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a catalog repository.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// UpsertManga stores canonical metadata.
func (r *Repository) UpsertManga(ctx context.Context, m Manga) (Manga, error) {
	m.Provider = strings.TrimSpace(m.Provider)
	m.ProviderID = strings.TrimSpace(m.ProviderID)
	if m.Provider == "" || m.ProviderID == "" {
		return Manga{}, errors.New("provider and provider_id are required")
	}
	synonyms, err := json.Marshal(cleanStrings(m.Synonyms))
	if err != nil {
		return Manga{}, fmt.Errorf("encode synonyms: %w", err)
	}
	var chapters any
	if m.Chapters != nil {
		chapters = *m.Chapters
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO catalog_manga (
			provider, provider_id, title_romaji, title_english, title_native,
			description, cover_image, status, format, chapters, synonyms_json,
			wanted, raw_json, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(provider, provider_id) DO UPDATE SET
			title_romaji = excluded.title_romaji,
			title_english = excluded.title_english,
			title_native = excluded.title_native,
			description = excluded.description,
			cover_image = excluded.cover_image,
			status = excluded.status,
			format = excluded.format,
			chapters = excluded.chapters,
			synonyms_json = excluded.synonyms_json,
			wanted = CASE WHEN excluded.wanted != 0 THEN excluded.wanted ELSE catalog_manga.wanted END,
			raw_json = excluded.raw_json,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id
	`, m.Provider, m.ProviderID, m.TitleRomaji, m.TitleEnglish, m.TitleNative, m.Description,
		m.CoverImage, m.Status, m.Format, chapters, string(synonyms), database.BoolToInt(m.Wanted), m.RawJSON)
	var id int64
	if err := row.Scan(&id); err != nil {
		return Manga{}, fmt.Errorf("upsert manga: %w", err)
	}
	return r.GetManga(ctx, id)
}

// GetManga returns one canonical manga row.
func (r *Repository) GetManga(ctx context.Context, id int64) (Manga, error) {
	row := r.db.QueryRowContext(ctx, mangaSelect()+` WHERE id = ?`, id)
	m, err := scanManga(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Manga{}, fmt.Errorf("catalog manga %d not found", id)
		}
		return Manga{}, fmt.Errorf("get manga: %w", err)
	}
	return m, nil
}

// ListWanted returns wanted canonical titles.
// UpdateWanted sets the wanted flag for one catalog manga.
func (r *Repository) UpdateWanted(ctx context.Context, id int64, wanted bool) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE catalog_manga SET wanted = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?
	`, database.BoolToInt(wanted), id)
	if err != nil {
		return fmt.Errorf("update wanted for manga %d: %w", id, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check wanted update for manga %d: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("manga %d not found", id)
	}
	return nil
}

func (r *Repository) ListWanted(ctx context.Context) ([]Manga, error) {
	rows, err := r.db.QueryContext(ctx, mangaSelect()+` WHERE wanted = 1 ORDER BY title_english COLLATE NOCASE, title_romaji COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list wanted: %w", err)
	}
	defer rows.Close()
	var out []Manga
	for rows.Next() {
		m, err := scanManga(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpsertMatch stores a source match.
func (r *Repository) UpsertMatch(ctx context.Context, m Match) (Match, error) {
	m.SourceID = strings.TrimSpace(m.SourceID)
	m.SourceURL = strings.TrimSpace(m.SourceURL)
	if m.CatalogMangaID <= 0 || m.SourceURL == "" {
		return Match{}, errors.New("catalog_manga_id and source_url are required")
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO title_source_matches (
			catalog_manga_id, source_id, source_url, title, confidence,
			match_method, chapters_found, sample_images_found, error,
			verified_at, updated_at
		) VALUES (?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(catalog_manga_id, source_url) DO UPDATE SET
			source_id = excluded.source_id,
			title = excluded.title,
			confidence = excluded.confidence,
			match_method = excluded.match_method,
			chapters_found = excluded.chapters_found,
			sample_images_found = excluded.sample_images_found,
			error = excluded.error,
			verified_at = excluded.verified_at,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id
	`, m.CatalogMangaID, m.SourceID, m.SourceURL, m.Title, m.Confidence, nonEmpty(m.MatchMethod, "slug_probe"), m.ChaptersFound, m.SampleImagesFound, m.Error)
	var id int64
	if err := row.Scan(&id); err != nil {
		return Match{}, fmt.Errorf("upsert match: %w", err)
	}
	return r.GetMatch(ctx, id)
}

// GetMatch returns one source match.
func (r *Repository) GetMatch(ctx context.Context, id int64) (Match, error) {
	row := r.db.QueryRowContext(ctx, matchSelect()+` WHERE id = ?`, id)
	m, err := scanMatch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Match{}, fmt.Errorf("source match %d not found", id)
		}
		return Match{}, fmt.Errorf("get source match: %w", err)
	}
	return m, nil
}

// ListMatches returns matches for one canonical title.
func (r *Repository) ListMatches(ctx context.Context, catalogMangaID int64) ([]Match, error) {
	rows, err := r.db.QueryContext(ctx, matchSelect()+`
		WHERE catalog_manga_id = ?
		ORDER BY chapters_found DESC, confidence DESC, source_id COLLATE NOCASE
	`, catalogMangaID)
	if err != nil {
		return nil, fmt.Errorf("list source matches: %w", err)
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		m, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func mangaSelect() string {
	return `
		SELECT id, provider, provider_id, title_romaji, title_english, title_native,
			description, cover_image, status, format, chapters, synonyms_json,
			wanted, raw_json, updated_at
		FROM catalog_manga`
}

func matchSelect() string {
	return `
		SELECT id, catalog_manga_id, COALESCE(source_id, ''), source_url, title,
			confidence, match_method, chapters_found, sample_images_found,
			error, COALESCE(verified_at, ''), updated_at
		FROM title_source_matches`
}

func scanManga(row interface{ Scan(...any) error }) (Manga, error) {
	var m Manga
	var chapters sql.NullInt64
	var synonymsJSON string
	var wanted int
	var updated string
	if err := row.Scan(&m.ID, &m.Provider, &m.ProviderID, &m.TitleRomaji, &m.TitleEnglish,
		&m.TitleNative, &m.Description, &m.CoverImage, &m.Status, &m.Format, &chapters,
		&synonymsJSON, &wanted, &m.RawJSON, &updated); err != nil {
		return Manga{}, err
	}
	if chapters.Valid {
		v := int(chapters.Int64)
		m.Chapters = &v
	}
	if err := json.Unmarshal([]byte(synonymsJSON), &m.Synonyms); err != nil {
		return Manga{}, fmt.Errorf("decode synonyms for manga %d: %w", m.ID, err)
	}
	m.Wanted = wanted != 0
	t, err := database.ParseTime(updated)
	if err != nil {
		return Manga{}, err
	}
	m.UpdatedAt = t
	return m, nil
}

func scanMatch(row interface{ Scan(...any) error }) (Match, error) {
	var m Match
	var updated string
	if err := row.Scan(&m.ID, &m.CatalogMangaID, &m.SourceID, &m.SourceURL, &m.Title,
		&m.Confidence, &m.MatchMethod, &m.ChaptersFound, &m.SampleImagesFound,
		&m.Error, &m.VerifiedAt, &updated); err != nil {
		return Match{}, err
	}
	t, err := database.ParseTime(updated)
	if err != nil {
		return Match{}, err
	}
	m.UpdatedAt = t
	return m, nil
}

func cleanStrings(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[strings.ToLower(v)] {
			continue
		}
		seen[strings.ToLower(v)] = true
		out = append(out, v)
	}
	return out
}

func nonEmpty(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
