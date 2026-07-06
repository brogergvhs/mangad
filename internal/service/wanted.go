package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"

	"github.com/brogergvhs/mangad/internal/browserdownload"
	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/ui"
	"github.com/brogergvhs/mangad/internal/util"
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
		anilist: catalog.NewAniListClient(nil),
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
func (s *WantedService) MatchSources(ctx context.Context, cfg *config.Config, logSvc ui.Log, catalogID int64) ([]catalog.Match, error) {
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
	var enabled []sources.Source
	for _, src := range sourceList {
		if src.Enabled {
			enabled = append(enabled, src)
		}
	}
	matches := make(chan catalog.Match, len(enabled))
	jobs := make(chan sources.Source)
	var wg sync.WaitGroup
	for range min(len(enabled), 4) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for src := range jobs {
				if match, ok := s.matchSource(ctx, cfg, logSvc, manga, src); ok {
					select {
					case matches <- match:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	for _, src := range enabled {
		select {
		case jobs <- src:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(matches)
			return nil, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	close(matches)

	var out []catalog.Match
	for match := range matches {
		stored, err := s.catalog.UpsertMatch(ctx, match)
		if err != nil {
			return out, err
		}
		out = append(out, stored)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].ChaptersFound != out[j].ChaptersFound {
			return out[i].ChaptersFound > out[j].ChaptersFound
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out, nil
}

func (s *WantedService) matchSource(ctx context.Context, cfg *config.Config, logSvc ui.Log, manga catalog.Manga, src sources.Source) (catalog.Match, bool) {
	probeCfg := ConfigForSource(*cfg, src, SourceConfigOptions{})
	candidates, searched := searchSourceURLs(ctx, probeCfg, logSvc, src, manga)
	method := "search"
	if !searched {
		method = "slug_probe"
		candidates = append(candidates, candidateSourceURLs(src, manga)...)
	} else if len(candidates) == 0 && logSvc != nil {
		logSvc.Debugf("Source search %s completed with no candidates; skipping guessed slug probes.\n", src.ID)
	}
	for _, candidate := range uniqueStrings(candidates) {
		match, ok := s.verifyCandidate(ctx, cfg, logSvc, manga, src, candidate, method)
		if ok {
			return match, true
		}
	}
	return catalog.Match{}, false
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

func (s *WantedService) verifyCandidate(ctx context.Context, cfg *config.Config, logSvc ui.Log, manga catalog.Manga, src sources.Source, sourceURL, method string) (catalog.Match, bool) {
	probeCfg := ConfigForSource(*cfg, src, SourceConfigOptions{})
	downloadSvc, err := NewSourceDownloadService(&probeCfg, logSvc, nil, src.Scraper)
	if err != nil {
		if logSvc != nil {
			logSvc.Debugf("Candidate %s skipped, scraper setup failed: %v\n", sourceURL, err)
		}
		return catalog.Match{}, false
	}
	chapters, err := downloadSvc.FetchChapters(ctx, sourceURL)
	if err != nil {
		if logSvc != nil {
			logSvc.Debugf("Candidate %s failed chapter fetch: %v\n", sourceURL, err)
		}
		return catalog.Match{}, false
	}
	if len(chapters) == 0 {
		if logSvc != nil {
			logSvc.Debugf("Candidate %s has no chapters.\n", sourceURL)
		}
		return catalog.Match{}, false
	}
	return catalog.Match{
		CatalogMangaID: manga.ID,
		SourceID:       src.ID,
		SourceURL:      sourceURL,
		Title:          displayMangaTitle(manga),
		Confidence:     matchConfidence(manga, sourceURL, len(chapters)),
		MatchMethod:    method,
		ChaptersFound:  len(chapters),
	}, true
}

func searchSourceURLs(ctx context.Context, cfg config.Config, logSvc ui.Log, src sources.Source, manga catalog.Manga) ([]string, bool) {
	if strings.TrimSpace(src.SearchURL) == "" {
		if logSvc != nil {
			logSvc.Debugf("Source search skipped for %s: no search_url.\n", src.ID)
		}
		return nil, false
	}
	var out []string
	searched := false
	for _, title := range mangaTitleVariants(manga) {
		searchURL := strings.ReplaceAll(src.SearchURL, "{query}", url.QueryEscape(title))
		if logSvc != nil {
			logSvc.Debugf("Source search %s query %q: %s\n", src.ID, title, searchURL)
		}
		body, err := fetchSearchPage(ctx, cfg, searchURL)
		if err != nil {
			if logSvc != nil {
				logSvc.Debugf("Source search failed for %s: %v\n", searchURL, err)
			}
			continue
		}
		searched = true
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
		if err != nil {
			if logSvc != nil {
				logSvc.Debugf("Source search parse failed for %s (%d bytes): %v\n", searchURL, len(body), err)
			}
			continue
		}
		found := append(searchLinks(doc, src, manga), searchStructuredLinks(body, src, manga)...)
		if logSvc != nil {
			logSvc.Debugf("Source search %s returned %d candidates from %d bytes.\n", src.ID, len(uniqueStrings(found)), len(body))
		}
		out = append(out, found...)
		if len(out) > 0 {
			break
		}
	}
	if len(out) == 0 && logSvc != nil {
		logSvc.Debugf("Source search %s found no candidates for %q.\n", src.ID, displayMangaTitle(manga))
	}
	return out, searched
}

func fetchSearchPage(ctx context.Context, cfg config.Config, target string) (string, error) {
	if cfg.BrowserDownload.Enabled {
		return browserdownload.New(cfg.BrowserDownload.Endpoint, time.Duration(cfg.BrowserDownload.TimeoutSeconds)*time.Second, nil).Fetch(ctx, target)
	}
	client, err := util.NewHTTPClient(util.HTTPClientOptions{
		Timeout:    30 * time.Second,
		UserAgent:  util.PickUserAgent(cfg.UserAgent),
		Cookie:     cfg.Cookie,
		CookieFile: cfg.CookieFile,
		RateLimit:  hostRateLimit(&cfg),
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	return string(data), err
}

func searchLinks(doc *goquery.Document, src sources.Source, manga catalog.Manga) []string {
	base, err := url.Parse(src.BaseURL)
	if err != nil {
		return nil
	}
	var out []string
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		resolved := resolveMatchURL(src.BaseURL, href)
		u, err := url.Parse(resolved)
		if err != nil || u.Host == "" || !sameHostURL(base, u) {
			return
		}
		text := strings.Join(strings.Fields(a.Text()), " ")
		if !looksLikeMangaResult(src, manga, resolved, text) {
			return
		}
		out = append(out, resolved)
	})
	return uniqueStrings(out)
}

func searchStructuredLinks(body string, src sources.Source, manga catalog.Manga) []string {
	var raw any
	if json.Unmarshal([]byte(body), &raw) != nil {
		return nil
	}
	var out []sourceCandidate
	var walk func(any)
	walk = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			text := jsonResultText(v)
			chapters := jsonNumber(v["chapter_count"])
			for _, key := range []string{"public_url", "permalink", "href"} {
				if candidate, ok := v[key].(string); ok {
					resolved := resolveMatchURL(src.BaseURL, candidate)
					if looksLikeMangaResult(src, manga, resolved, text) {
						out = append(out, sourceCandidate{url: resolved, chapters: chapters})
					}
				}
			}
			if slug, ok := v["slug"].(string); ok {
				if candidate := sourceURLFromSlug(src, slug); candidate != "" && looksLikeMangaResult(src, manga, candidate, text) {
					out = append(out, sourceCandidate{url: candidate, chapters: chapters})
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(raw)
	sort.SliceStable(out, func(i, j int) bool { return out[i].chapters > out[j].chapters })
	values := make([]string, 0, len(out))
	for _, candidate := range out {
		values = append(values, candidate.url)
	}
	return uniqueStrings(values)
}

type sourceCandidate struct {
	url      string
	chapters int
}

func jsonNumber(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(strings.TrimSpace(n))
		return i
	default:
		return 0
	}
}

func jsonResultText(value any) string {
	var parts []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			parts = append(parts, x)
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			for key, item := range x {
				switch key {
				case "title", "titles", "name", "slug", "limited_titles":
					walk(item)
				}
			}
		}
	}
	walk(value)
	return strings.Join(parts, " ")
}

func sourceURLFromSlug(src sources.Source, slug string) string {
	base, err := url.Parse(src.BaseURL)
	if err != nil || base.Host == "" {
		return ""
	}
	sample, err := url.Parse(src.SampleMangaURL)
	if err != nil {
		return ""
	}
	parts := pathParts(sample.Path)
	if len(parts) == 0 {
		return ""
	}
	base.Path = "/" + path.Join(parts[0], strings.Trim(slug, "/"))
	base.RawQuery = ""
	base.Fragment = ""
	return base.String()
}

func looksLikeMangaResult(src sources.Source, manga catalog.Manga, href, text string) bool {
	if !matchesWantedTitle(manga, href+" "+text) {
		return false
	}
	u, err := url.Parse(href)
	if err != nil {
		return false
	}
	sample, err := url.Parse(src.SampleMangaURL)
	if err != nil {
		return true
	}
	candidateParts, sampleParts := pathParts(u.Path), pathParts(sample.Path)
	if len(candidateParts) == 0 || len(sampleParts) == 0 {
		return false
	}
	return candidateParts[0] == sampleParts[0] && len(candidateParts) == len(sampleParts)
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

func resolveMatchURL(baseURL, href string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return href
	}
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return href
	}
	u.Fragment = ""
	return base.ResolveReference(u).String()
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

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func matchesWantedTitle(m catalog.Manga, value string) bool {
	value = strings.ToLower(value)
	for _, title := range mangaTitleVariants(m) {
		words := strings.Fields(strings.ReplaceAll(slugify(title), "-", " "))
		if len(words) == 0 {
			continue
		}
		hits := 0
		for _, word := range words {
			if len(word) >= 3 && strings.Contains(value, word) {
				hits++
			}
		}
		if hits >= len(words)/2+1 {
			return true
		}
	}
	return false
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

func sameHostURL(a, b *url.URL) bool {
	return strings.TrimPrefix(strings.ToLower(a.Host), "www.") == strings.TrimPrefix(strings.ToLower(b.Host), "www.")
}
