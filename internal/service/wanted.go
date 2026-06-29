package service

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/ui"
)

// WantedService coordinates canonical manga and source matching.
type WantedService struct {
	catalog *catalog.Repository
	sources *sources.Repository
	library *library.Repository
	anilist interface {
		Search(context.Context, string, int) ([]catalog.Manga, error)
		Get(context.Context, int) (catalog.Manga, error)
	}
}

// OpenWanted opens the app database for wanted-title workflows.
func OpenWanted(ctx context.Context, dbPath string) (*WantedService, func(), error) {
	db, err := database.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, err
	}
	if err := database.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return newWantedService(db), func() { _ = db.Close() }, nil
}

func newWantedService(db *sql.DB) *WantedService {
	return &WantedService{
		catalog: catalog.NewRepository(db),
		sources: sources.NewRepository(db),
		library: library.NewRepository(db),
		anilist: catalog.NewAniListClient(http.DefaultClient),
	}
}

// SearchAniList searches AniList and stores returned metadata locally.
func (s *WantedService) SearchAniList(ctx context.Context, query string, limit int) ([]catalog.Manga, error) {
	items, err := s.anilist.Search(ctx, strings.TrimSpace(query), limit)
	if err != nil {
		return nil, err
	}
	out := make([]catalog.Manga, 0, len(items))
	for _, item := range items {
		stored, err := s.catalog.UpsertManga(ctx, item)
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return out, nil
}

// AddAniListWanted fetches an AniList title, stores it, and marks it wanted.
func (s *WantedService) AddAniListWanted(ctx context.Context, anilistID int) (catalog.Manga, error) {
	item, err := s.anilist.Get(ctx, anilistID)
	if err != nil {
		return catalog.Manga{}, err
	}
	item.Wanted = true
	return s.catalog.UpsertManga(ctx, item)
}

// ListWanted returns wanted canonical titles.
func (s *WantedService) ListWanted(ctx context.Context) ([]catalog.Manga, error) {
	return s.catalog.ListWanted(ctx)
}

// MatchSources finds source candidates for one wanted title.
func (s *WantedService) MatchSources(ctx context.Context, cfg *config.Config, logSvc *ui.Logger, catalogID int64) ([]catalog.Match, error) {
	manga, err := s.catalog.GetManga(ctx, catalogID)
	if err != nil {
		return nil, err
	}
	if profiles, err := sources.BuiltInProfiles(); err == nil {
		if err := s.sources.Sync(ctx, profiles, sources.OriginBuiltin); err != nil {
			return nil, err
		}
	}
	sourceList, err := s.sources.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []catalog.Match
	for _, src := range sourceList {
		if !src.Enabled {
			continue
		}
		for _, candidate := range candidateSourceURLs(src, manga) {
			match, ok := s.verifyCandidate(ctx, cfg, logSvc, manga, src, candidate)
			if !ok {
				continue
			}
			stored, err := s.catalog.UpsertMatch(ctx, match)
			if err != nil {
				return out, err
			}
			out = append(out, stored)
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ChaptersFound != out[j].ChaptersFound {
			return out[i].ChaptersFound > out[j].ChaptersFound
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out, nil
}

// ListMatches returns persisted source matches.
func (s *WantedService) ListMatches(ctx context.Context, catalogID int64) ([]catalog.Match, error) {
	return s.catalog.ListMatches(ctx, catalogID)
}

// TrackMatch adds a selected source match to the library.
func (s *WantedService) TrackMatch(ctx context.Context, matchID int64, outputPath string, monitored bool, refreshInterval string) (library.Title, error) {
	match, err := s.catalog.GetMatch(ctx, matchID)
	if err != nil {
		return library.Title{}, err
	}
	manga, err := s.catalog.GetManga(ctx, match.CatalogMangaID)
	if err != nil {
		return library.Title{}, err
	}
	return s.library.AddTitle(ctx, library.AddTitleParams{
		CatalogMangaID:  &manga.ID,
		SourceID:        match.SourceID,
		SourceURL:       match.SourceURL,
		DisplayTitle:    displayMangaTitle(manga),
		OutputPath:      outputPath,
		Monitored:       monitored,
		RefreshInterval: refreshInterval,
	})
}

func (s *WantedService) verifyCandidate(ctx context.Context, cfg *config.Config, logSvc *ui.Logger, manga catalog.Manga, src sources.Source, sourceURL string) (catalog.Match, bool) {
	probeCfg := *cfg
	probeCfg.AllowExt = src.AllowedExtensions
	if src.RequiresBrowserSolver {
		probeCfg.BrowserSolver.Enabled = true
	}
	if src.RequiresBrowserDownload {
		probeCfg.BrowserDownload.Enabled = true
	}
	downloadSvc, err := NewDefaultDownloadService(&probeCfg, logSvc, nil)
	if err != nil {
		return catalog.Match{}, false
	}
	chapters, err := downloadSvc.FetchChapters(ctx, sourceURL)
	if err != nil || len(chapters) == 0 {
		return catalog.Match{}, false
	}
	return catalog.Match{
		CatalogMangaID: manga.ID,
		SourceID:       src.ID,
		SourceURL:      sourceURL,
		Title:          displayMangaTitle(manga),
		Confidence:     matchConfidence(manga, sourceURL, len(chapters)),
		MatchMethod:    "slug_probe",
		ChaptersFound:  len(chapters),
	}, true
}

func candidateSourceURLs(src sources.Source, manga catalog.Manga) []string {
	u, err := url.Parse(src.SampleMangaURL)
	if err != nil || u.Host == "" {
		return nil
	}
	parts := pathParts(u.Path)
	if len(parts) == 0 || hasNumericOrOpaqueNeighbor(parts) {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, title := range mangaTitleVariants(manga) {
		slug := slugify(title)
		if slug == "" {
			continue
		}
		next := *u
		nextParts := append([]string{}, parts...)
		nextParts[len(nextParts)-1] = slug
		next.Path = "/" + path.Join(nextParts...)
		next.RawQuery = ""
		value := next.String()
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
		if len(out) >= 4 {
			break
		}
	}
	return out
}

func mangaTitleVariants(m catalog.Manga) []string {
	return cleanMatchStrings(append([]string{m.TitleEnglish, m.TitleRomaji, m.TitleNative}, m.Synonyms...))
}

func cleanMatchStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func displayMangaTitle(m catalog.Manga) string {
	for _, value := range []string{m.TitleEnglish, m.TitleRomaji, m.TitleNative} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return m.Provider + ":" + m.ProviderID
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func hasNumericOrOpaqueNeighbor(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	prev := parts[len(parts)-2]
	if _, err := strconv.Atoi(prev); err == nil {
		return true
	}
	return regexp.MustCompile(`^[0-9A-Za-z]{12,}$`).MatchString(prev)
}

func matchConfidence(m catalog.Manga, sourceURL string, chapters int) float64 {
	score := 0.5
	urlSlug := strings.ToLower(sourceURL)
	for _, title := range mangaTitleVariants(m) {
		if slug := slugify(title); slug != "" && strings.Contains(urlSlug, slug) {
			score += 0.35
			break
		}
	}
	if m.Chapters != nil && *m.Chapters > 0 {
		diff := *m.Chapters - chapters
		if diff < 0 {
			diff = -diff
		}
		if diff <= 2 {
			score += 0.15
		}
	} else if chapters > 0 {
		score += 0.05
	}
	if score > 1 {
		return 1
	}
	return score
}

func pathParts(p string) []string {
	raw := strings.Split(strings.Trim(p, "/"), "/")
	out := raw[:0]
	for _, part := range raw {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
