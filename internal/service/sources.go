package service

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"

	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/ui"
)

type sourceService struct {
	repo *sources.Repository
}

// SourceVerifyResult is the latest verification sample for a source.
type SourceVerifyResult struct {
	SourceID        string   `json:"source_id"`
	Status          string   `json:"status"`
	ChaptersFound   int      `json:"chapters_found"`
	ImagesFound     int      `json:"images_found"`
	ImageExtensions []string `json:"image_extensions"`
	ChapterFetch    string   `json:"chapter_fetch,omitempty"`
	ImageFetch      string   `json:"image_fetch,omitempty"`
	Error           string   `json:"error,omitempty"`
}

func newSourceService(db *sql.DB) *sourceService {
	return &sourceService{repo: sources.NewRepository(db)}
}

// SourceTestResult is a live probe of a candidate (possibly unsaved) source.
type SourceTestResult struct {
	ChaptersFound   int      `json:"chapters_found"`
	SampleChapter   string   `json:"sample_chapter,omitempty"`
	SampleURL       string   `json:"sample_url,omitempty"`
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
	res.SampleURL = first.URL
	images, err := svc.FetchImages(ctx, first)
	if err != nil {
		res.Error = fmt.Sprintf("chapters read OK, but the first chapter failed: %v", err)
		return res
	}
	res.Images = images
	res.ImageExtensions = imageExtensions(images)
	switch {
	case len(images) > 0:
		res.ImageFetch = sources.FetchHTTP
	case useBrowser:
		res.ImageFetch = sources.FetchBrowser
		res.Error = "no image URLs in the chapter HTML — images will be captured by the browser downloader"
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
	if err := s.repo.UpdateCheck(ctx, src.ID, result.Status, result.Error, result.ChaptersFound, result.ImagesFound, result.ImageExtensions, result.ChapterFetch, result.ImageFetch); err != nil {
		return result, err
	}
	if checkErr != nil {
		return result, checkErr
	}
	return result, nil
}

func (s *sourceService) verify(ctx context.Context, cfg *config.Config, logSvc ui.Log, src sources.Source, result *SourceVerifyResult) error {
	if !src.Enabled {
		return fmt.Errorf("source %s is disabled", src.ID)
	}

	// Try plain HTTP first; escalate to the solver when the HTML can't be read
	// OR when a sample image won't download (e.g. a CDN that needs warmed
	// cookies). Escalating on image failure means the learned solver method
	// carries through to real downloads.
	attempts := []bool{false}
	if cfg.BrowserSolver.Enabled {
		attempts = append(attempts, true)
	}
	var lastErr error
	for _, useSolver := range attempts {
		if err := s.verifyOnce(ctx, cfg, logSvc, src, useSolver, result); err != nil {
			lastErr = err
			result.Status = fetchFailureStatus(err)
			continue
		}
		return nil
	}
	return lastErr
}

// verifyOnce fetches the chapter list, extracts a sample chapter's images, and
// downloads one image to prove the whole pipeline works with the given method.
func (s *sourceService) verifyOnce(ctx context.Context, cfg *config.Config, logSvc ui.Log, src sources.Source, useSolver bool, result *SourceVerifyResult) error {
	probe := probeConfig(*cfg, src, useSolver, false)
	svc, err := NewSourceDownloadService(&probe, logSvc, nil, src.Scraper)
	if err != nil {
		return err
	}
	method := sources.FetchHTTP
	if useSolver {
		method = sources.FetchSolver
	}

	list, err := svc.FetchChapters(ctx, src.SampleMangaURL)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return fmt.Errorf("no chapters found")
	}
	result.ChapterFetch = method
	result.ChaptersFound = len(list)
	if len(list) < src.MinChapters {
		result.Status = sources.StatusDegraded
		return fmt.Errorf("found %d chapters, expected at least %d", len(list), src.MinChapters)
	}

	images, err := svc.FetchImages(ctx, list[0])
	if err != nil {
		return err
	}
	result.ImagesFound = len(images)
	result.ImageExtensions = imageExtensions(images)
	if len(images) == 0 {
		if cfg.BrowserDownload.Enabled {
			result.ImageFetch = sources.FetchBrowser
			result.Status = sources.StatusHealthy
			return nil
		}
		return fmt.Errorf("no images found in sample chapter")
	}
	// Actually download a sample image so CDN 403s surface here, not at download.
	if err := svc.VerifyImage(ctx, images[0], list[0].URL); err != nil {
		result.ImageFetch = ""
		return fmt.Errorf("sample image download failed: %w", err)
	}
	result.ImageFetch = sources.FetchHTTP
	result.Status = sources.StatusHealthy
	return nil
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
