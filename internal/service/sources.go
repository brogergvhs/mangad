package service

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"net/url"
	"path"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/ui"
)

type sourceService struct {
	repo *sources.Repository
}

// SourceVerifyResult is the latest verification sample for a source.
type SourceVerifyResult struct {
	SourceID        string               `json:"source_id"`
	Status          string               `json:"status"`
	ChaptersFound   int                  `json:"chapters_found"`
	ImagesFound     int                  `json:"images_found"`
	ImageExtensions []string             `json:"image_extensions"`
	Steps           []sources.VerifyStep `json:"steps,omitempty"`
	ChapterFetch    string               `json:"chapter_fetch,omitempty"`
	ImageFetch      string               `json:"image_fetch,omitempty"`
	Error           string               `json:"error,omitempty"`
}

func newSourceService(db *sql.DB) *sourceService {
	return &sourceService{repo: sources.NewRepository(db)}
}

// SourceTestResult is a live probe of a candidate (possibly unsaved) source.
type SourceTestResult struct {
	ChaptersFound   int      `json:"chapters_found"`
	SampleChapter   string   `json:"sample_chapter,omitempty"`
	Images          []string `json:"images,omitempty"`
	ImageExtensions []string `json:"image_extensions,omitempty"`
	ChapterFetch    string   `json:"chapter_fetch"`
	ImageFetch      string   `json:"image_fetch,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// TestProfile scrapes the profile's sample manga URL with the chosen fetch
// methods and reports what it found, without persisting anything.
func (s *sourceService) TestProfile(ctx context.Context, cfg *config.Config, logSvc ui.Log, profile sources.Profile, useSolver, useBrowser bool) SourceTestResult {
	src := sources.Source{Profile: profile}
	res := SourceTestResult{ChapterFetch: sources.FetchHTTP}
	if useSolver {
		res.ChapterFetch = sources.FetchSolver
	}
	probe := probeConfig(*cfg, src, useSolver, useBrowser)
	svc, err := NewSourceDownloadService(&probe, logSvc, nil, profile.Scraper)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	list, err := svc.FetchChapters(ctx, profile.SampleMangaURL)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.ChaptersFound = len(list)
	if len(list) == 0 {
		res.Error = "no chapters found — the site may need FlareSolverr, or the manga URL is wrong"
		return res
	}
	first := list[0]
	res.SampleChapter = first.Label
	if res.SampleChapter == "" {
		res.SampleChapter = first.Title
	}
	images, err := svc.FetchImages(ctx, first)
	if err != nil {
		res.Error = fmt.Sprintf("chapters read OK, but the first chapter failed: %v", err)
		return res
	}
	res.Images = images
	res.ImageExtensions = imageExtensions(images)
	switch {
	case useBrowser:
		// The user chose browser capture; that's what the source will use.
		res.ImageFetch = sources.FetchBrowser
		if len(images) == 0 {
			res.Error = "no image URLs in the chapter HTML — images will be captured by the browser downloader"
		}
	case len(images) > 0:
		res.ImageFetch = sources.FetchHTTP
	default:
		res.Error = "no image URLs in the chapter HTML — enable browser image download to fetch them"
	}
	return res
}

func (s *sourceService) SyncBuiltIn(ctx context.Context) error {
	profiles, err := sources.BuiltInProfiles()
	if err != nil {
		return err
	}
	return s.repo.Sync(ctx, profiles, sources.OriginBuiltin)
}

func (s *sourceService) SyncRegistry(ctx context.Context, registryURL string) error {
	profiles, err := sources.FetchRegistry(ctx, registryURL)
	if err != nil {
		return err
	}
	return s.repo.Sync(ctx, profiles, sources.OriginRegistry)
}

func (s *sourceService) ListSources(ctx context.Context) ([]sources.Source, error) {
	return s.repo.List(ctx)
}

func (s *sourceService) GetSource(ctx context.Context, id string) (sources.Source, error) {
	return s.repo.Get(ctx, strings.TrimSpace(id))
}

func (s *sourceService) ImportLocal(ctx context.Context, profile sources.Profile) error {
	return s.repo.Sync(ctx, []sources.Profile{profile}, sources.OriginLocal)
}

// SetFetchMethods overrides a source's chapter/image fetch methods. Empty means
// "auto". chapter is http|solver; image is http|browser.
func (s *sourceService) SetFetchMethods(ctx context.Context, id, chapterFetch, imageFetch string) error {
	chapterFetch = strings.TrimSpace(chapterFetch)
	imageFetch = strings.TrimSpace(imageFetch)
	switch chapterFetch {
	case "", sources.FetchHTTP, sources.FetchSolver:
	default:
		return fmt.Errorf("invalid chapter fetch method %q", chapterFetch)
	}
	switch imageFetch {
	case "", sources.FetchHTTP, sources.FetchBrowser:
	default:
		return fmt.Errorf("invalid image fetch method %q", imageFetch)
	}
	return s.repo.SetFetchMethods(ctx, id, chapterFetch, imageFetch)
}

func (s *sourceService) RemoveLocal(ctx context.Context, id string) error {
	return s.repo.RemoveLocal(ctx, strings.TrimSpace(id))
}

func (s *sourceService) ExportSource(ctx context.Context, id string) ([]byte, error) {
	src, err := s.repo.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	return sources.EncodeProfileYAML(src.Profile)
}

func (s *sourceService) VerifySource(ctx context.Context, cfg *config.Config, logSvc ui.Log, id string) (SourceVerifyResult, error) {
	src, err := s.repo.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return SourceVerifyResult{}, err
	}
	result := SourceVerifyResult{SourceID: src.ID, Status: sources.StatusBroken}
	checkErr := s.verify(ctx, cfg, logSvc, src, &result)
	if checkErr != nil {
		result.Error = checkErr.Error()
	}
	if err := s.repo.UpdateCheck(ctx, src.ID, sources.CheckResult{
		Status: result.Status, LastError: result.Error,
		ChaptersFound: result.ChaptersFound, ImagesFound: result.ImagesFound,
		ImageExtensions: result.ImageExtensions, Steps: result.Steps,
		ChapterFetch: result.ChapterFetch, ImageFetch: result.ImageFetch,
	}); err != nil {
		return result, err
	}
	if checkErr != nil {
		return result, checkErr
	}
	return result, nil
}

// verify runs the verification pipeline: search the site, pick a random result
// manga, fetch its chapter list, then fetch and download images of a random
// chapter. Each stage is recorded as a step; fetch stages escalate
// http -> solver so the learned method carries through to real downloads.
func (s *sourceService) verify(ctx context.Context, cfg *config.Config, logSvc ui.Log, src sources.Source, result *SourceVerifyResult) error {
	if !src.Enabled {
		return fmt.Errorf("source %s is disabled", src.ID)
	}

	searchStep, pickStep, mangaURL := s.verifySearch(ctx, cfg, logSvc, src)

	attempts := []bool{false}
	if cfg.BrowserSolver.Enabled || src.ChapterFetch == sources.FetchSolver || src.RequiresBrowserSolver {
		attempts = append(attempts, true)
	}
	if src.ChapterFetch == sources.FetchSolver {
		attempts = []bool{true} // source is pinned to the solver; don't waste an http probe
	}
	var chapStep, imgStep sources.VerifyStep
	var lastErr error
	for _, useSolver := range attempts {
		chapStep, imgStep, lastErr = s.verifyFetch(ctx, cfg, logSvc, src, mangaURL, useSolver, result)
		if lastErr == nil {
			break
		}
	}
	result.Steps = []sources.VerifyStep{searchStep, pickStep, chapStep, imgStep}
	if lastErr != nil {
		result.Status = fetchFailureStatus(lastErr)
		return lastErr
	}
	if searchStep.Status == sources.StepFailed {
		// Downloads work but the site can't be searched for new matches.
		result.Status = sources.StatusDegraded
		return nil
	}
	result.Status = sources.StatusHealthy
	return nil
}

// verifySearch checks the source's search page and picks a random result to
// verify against; sources without search fall back to the sample manga.
func (s *sourceService) verifySearch(ctx context.Context, cfg *config.Config, logSvc ui.Log, src sources.Source) (search, pick sources.VerifyStep, mangaURL string) {
	search = sources.VerifyStep{Name: "search", Status: sources.StepSkipped}
	pick = sources.VerifyStep{Name: "pick manga", Status: sources.StepSkipped, Detail: "using sample manga"}
	mangaURL = src.SampleMangaURL
	if src.SingleManga || strings.TrimSpace(src.SearchURL) == "" {
		search.Detail = "source has no search"
		return search, pick, mangaURL
	}

	query := sampleTitleQuery(src)
	searchURL := strings.ReplaceAll(src.SearchURL, "{query}", url.QueryEscape(query))
	search.Detail = fmt.Sprintf("%q", query)
	body, finalURL, err := fetchSearchPage(ctx, *cfg, searchURL)
	if err != nil {
		search.Status = sources.StepFailed
		search.Log = fmt.Sprintf("GET %s: %v", searchURL, err)
		pick.Detail = "falling back to sample manga"
		return search, pick, mangaURL
	}
	// Reuse the matcher's result parsing with a synthetic manga titled after
	// the sample slug, so at least the sample manga should be found.
	fake := catalog.Manga{TitleRomaji: query}
	var links []string
	if doc, err := goquery.NewDocumentFromReader(strings.NewReader(body)); err == nil {
		links = searchLinks(doc, src, fake)
	}
	links = uniqueStrings(append(links, searchStructuredLinks(body, src, fake)...))
	if len(links) == 0 && finalURL != searchURL && looksLikeMangaResult(src, fake, finalURL, "") {
		// A single-result search redirected straight to the manga page.
		links = []string{finalURL}
	}
	if len(links) == 0 {
		search.Status = sources.StepFailed
		search.Log = fmt.Sprintf("GET %s returned %d bytes but no usable manga results for %q", searchURL, len(body), query)
		pick.Detail = "falling back to sample manga"
		return search, pick, mangaURL
	}
	search.Status = sources.StepOK
	search.Detail = fmt.Sprintf("%d results for %q", len(links), query)
	mangaURL = links[rand.Intn(len(links))]
	pick.Status = sources.StepOK
	pick.Detail = mangaURL
	return search, pick, mangaURL
}

// verifyFetch fetches the chapter list and downloads one image of a random
// chapter with the given method, recording both stages as steps.
func (s *sourceService) verifyFetch(ctx context.Context, cfg *config.Config, logSvc ui.Log, src sources.Source, mangaURL string, useSolver bool, result *SourceVerifyResult) (chap, img sources.VerifyStep, err error) {
	chap = sources.VerifyStep{Name: "chapters", Status: sources.StepFailed}
	img = sources.VerifyStep{Name: "images", Status: sources.StepSkipped}
	probe := probeConfig(*cfg, src, useSolver, false)
	svc, err := NewSourceDownloadService(&probe, logSvc, nil, src.Scraper)
	if err != nil {
		chap.Log = err.Error()
		return chap, img, err
	}
	method := sources.FetchHTTP
	if useSolver {
		method = sources.FetchSolver
	}

	list, err := svc.FetchChapters(ctx, mangaURL)
	if err == nil && len(list) == 0 {
		err = fmt.Errorf("no chapters found at %s", mangaURL)
	}
	if err != nil {
		chap.Log = err.Error()
		return chap, img, err
	}
	chap.Status = sources.StepOK
	chap.Detail = fmt.Sprintf("%d chapters", len(list))
	result.ChapterFetch = method
	result.ChaptersFound = len(list)

	ch := list[rand.Intn(len(list))]
	if src.ImageFetch == sources.FetchBrowser || src.RequiresBrowserDownload {
		// Images are pinned to browser capture; the http probe doesn't apply
		// and must not relearn "http" over the user's choice.
		images, _ := svc.FetchImages(ctx, ch)
		result.ImagesFound = len(images)
		result.ImageExtensions = imageExtensions(images)
		result.ImageFetch = sources.FetchBrowser
		img.Status = sources.StepOK
		img.Detail = fmt.Sprintf("chapter %s: browser capture", ch.Label)
		return chap, img, nil
	}
	img.Status = sources.StepFailed
	images, err := svc.FetchImages(ctx, ch)
	if err != nil {
		img.Log = err.Error()
		return chap, img, err
	}
	result.ImagesFound = len(images)
	result.ImageExtensions = imageExtensions(images)
	if len(images) == 0 {
		if cfg.BrowserDownload.Enabled {
			img.Status = sources.StepOK
			img.Detail = fmt.Sprintf("chapter %s: browser capture", ch.Label)
			result.ImageFetch = sources.FetchBrowser
			return chap, img, nil
		}
		err = fmt.Errorf("no images found in chapter %s", ch.Label)
		img.Log = err.Error()
		return chap, img, err
	}
	// Actually download a sample image so CDN 403s surface here, not at download.
	if err := svc.VerifyImage(ctx, images[rand.Intn(len(images))], ch.URL); err != nil {
		result.ImageFetch = ""
		img.Log = err.Error()
		return chap, img, fmt.Errorf("sample image download failed: %w", err)
	}
	img.Status = sources.StepOK
	img.Detail = fmt.Sprintf("%d images in chapter %s", len(images), ch.Label)
	result.ImageFetch = sources.FetchHTTP
	return chap, img, nil
}

// sampleTitleQuery derives a search query from the sample manga URL's slug.
func sampleTitleQuery(src sources.Source) string {
	u, err := url.Parse(src.SampleMangaURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	slug := parts[len(parts)-1]
	slug, _, _ = strings.Cut(slug, ".") // "title.26697" -> "title"
	words := strings.FieldsFunc(slug, func(r rune) bool { return r == '-' || r == '_' })
	return strings.ToLower(strings.Join(words, " "))
}

// fetchFailureStatus maps a fetch error to a health status: a Cloudflare wall
// is distinct from a generically broken source.
func fetchFailureStatus(err error) string {
	if strings.Contains(strings.ToLower(err.Error()), "cloudflare") {
		return sources.StatusRequiresCF
	}
	return sources.StatusBroken
}

func imageExtensions(images []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, image := range images {
		image, _, _ = strings.Cut(image, "?")
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(image)), ".")
		if ext == "" || seen[ext] {
			continue
		}
		seen[ext] = true
		out = append(out, ext)
	}
	return out
}
