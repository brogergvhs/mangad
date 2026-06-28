// Package service contains reusable application use cases shared by the CLI and
// the future daemon/web UI.
package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brogergvhs/mangad/internal/browserfetch"
	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/downloader"
	"github.com/brogergvhs/mangad/internal/providers"
	"github.com/brogergvhs/mangad/internal/providers/generic"
	"github.com/brogergvhs/mangad/internal/ui"
	"github.com/brogergvhs/mangad/internal/util"

	cloudflarebp "github.com/DaRealFreak/cloudflare-bp-go"
)

// Logger is the logging contract needed by download services.
type Logger interface {
	Debugf(format string, args ...any)
	Errorf(format string, args ...any)
}

// ProgressHandle receives per-chapter download progress.
type ProgressHandle interface {
	SetTotal(total int)
	Update(done, total int, bytes int64)
	MarkDone()
}

// ProgressManager creates progress handles for concurrently downloaded chapters.
type ProgressManager interface {
	Register(prefix string) ProgressHandle
	Close()
}

// ChapterSelection describes how chapter lists should be filtered.
type ChapterSelection struct {
	Chapter      string
	Range        string
	ExcludeRange string
	List         string
	ExcludeList  string
}

// DownloadSummary describes the outcome of a batch download.
type DownloadSummary struct {
	Chapters       int64
	Images         int64
	Bytes          int64
	FailedChapters int64
	Errors         []string
	Duration       time.Duration
}

// ChapterDownloadResult describes one completed chapter download.
type ChapterDownloadResult struct {
	Chapter    chapters.Chapter
	OutputFile string
	Images     int
	Bytes      int64
}

// DownloadService coordinates scraping, chapter selection, and CBZ downloads.
type DownloadService struct {
	cfg      *config.Config
	client   *http.Client
	scraper  providers.Scraper
	log      Logger
	progress ProgressManager
}

// NewDownloadService creates a service from explicit dependencies.
func NewDownloadService(
	cfg *config.Config,
	client *http.Client,
	scraper providers.Scraper,
	log Logger,
	progress ProgressManager,
) *DownloadService {
	if log == nil {
		log = noopLogger{}
	}

	return &DownloadService{
		cfg:      cfg,
		client:   client,
		scraper:  scraper,
		log:      log,
		progress: progress,
	}
}

// SetProgressManager sets or replaces the progress manager used by Download.
func (s *DownloadService) SetProgressManager(progress ProgressManager) {
	s.progress = progress
}

// NewDefaultDownloadService creates the default HTTP client and generic scraper.
func NewDefaultDownloadService(
	cfg *config.Config,
	log *ui.Logger,
	progress ProgressManager,
) (*DownloadService, error) {
	browserState := &util.BrowserState{}
	client, err := util.NewHTTPClient(util.HTTPClientOptions{
		Timeout:     30 * time.Second,
		UserAgent:   util.PickUserAgent(cfg.UserAgent),
		Cookie:      cfg.Cookie,
		Transport:   cloudflarebp.AddCloudFlareByPass(http.DefaultTransport),
		CookieFile:  cfg.CookieFile,
		State:       browserState,
		DebugLogger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}

	var browser generic.BrowserFetcher
	if cfg.BrowserSolver.Enabled {
		if cfg.BrowserSolver.Provider != browserfetch.ProviderFlareSolverr {
			return nil, fmt.Errorf("unsupported browser solver provider %q", cfg.BrowserSolver.Provider)
		}
		timeout := time.Duration(cfg.BrowserSolver.TimeoutSeconds) * time.Second
		browser = flaresolverrFetcher{
			client:   browserfetch.NewFlareSolverr(cfg.BrowserSolver.Endpoint, timeout, nil),
			http:     client,
			state:    browserState,
			cache:    browserCookieCache{dbPath: cookieDBPath(cfg)},
			endpoint: cfg.BrowserSolver.Endpoint,
			timeout:  timeout,
			log:      log,
		}
	}
	scraper := generic.NewScraper(client, log, cfg.AllowExt, cfg.CheckJS, browser)

	return NewDownloadService(cfg, client, scraper, log, progress), nil
}

func cookieDBPath(cfg *config.Config) string {
	if cfg.CookieDBPath != "" {
		return cfg.CookieDBPath
	}
	return database.DefaultPath()
}

type flaresolverrFetcher struct {
	client   *browserfetch.FlareSolverr
	http     *http.Client
	state    *util.BrowserState
	cache    browserCookieCache
	endpoint string
	timeout  time.Duration
	log      *ui.Logger
}

func (f flaresolverrFetcher) LoadCached(ctx context.Context, target string) {
	session, err := f.cache.load(ctx, target)
	if err != nil {
		if f.log != nil {
			f.log.Debugf("Browser cookie cache load failed for %s: %v\n", target, err)
		}
		return
	}
	if session.userAgent != "" {
		f.state.SetUserAgent(session.userAgent)
	}
	if f.http != nil && f.http.Jar != nil && len(session.cookies) > 0 {
		u, err := url.Parse(target)
		if err == nil {
			f.http.Jar.SetCookies(u, session.cookies)
		}
	}
	if f.log != nil && len(session.cookies) > 0 {
		f.log.Infof("Loaded %d cached browser cookies for %s.\n", len(session.cookies), target)
	}
}

func (f flaresolverrFetcher) Fetch(ctx context.Context, target string) (string, error) {
	start := time.Now()
	if f.log != nil {
		f.log.Infof("FlareSolverr request.get %s via %s (timeout=%s).\n", target, f.endpoint, f.timeout)
	}
	result, err := f.client.Fetch(ctx, target)
	if err != nil {
		if f.log != nil {
			f.log.Errorf("FlareSolverr failed for %s after %s: %v\n", target, time.Since(start).Round(time.Millisecond), err)
		}
		return "", err
	}
	if f.log != nil {
		f.log.Infof("FlareSolverr solved %s with status %d in %s (%d bytes).\n", target, result.Status, time.Since(start).Round(time.Millisecond), len(result.HTML))
	}
	f.applySession(ctx, target, result)
	return result.HTML, nil
}

func (f flaresolverrFetcher) applySession(ctx context.Context, target string, result browserfetch.Result) {
	if result.UserAgent != "" {
		f.state.SetUserAgent(result.UserAgent)
		if f.log != nil {
			f.log.Infof("Using FlareSolverr user-agent for follow-up requests.\n")
		}
	}
	if f.http == nil || f.http.Jar == nil || len(result.Cookies) == 0 {
		return
	}
	rawURL := result.URL
	if rawURL == "" {
		rawURL = target
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return
	}
	f.http.Jar.SetCookies(u, result.Cookies)
	if f.log != nil {
		f.log.Infof("Stored %d FlareSolverr cookies for follow-up requests.\n", len(result.Cookies))
	}
	if err := f.cache.save(ctx, rawURL, result.UserAgent, result.Cookies); err != nil {
		if f.log != nil {
			f.log.Debugf("Browser cookie cache save failed for %s: %v\n", rawURL, err)
		}
	} else if f.log != nil {
		f.log.Infof("Cached %d FlareSolverr cookies for future runs.\n", len(result.Cookies))
	}
}

// FetchChapters fetches and normalizes all chapters for a source URL.
func (s *DownloadService) FetchChapters(ctx context.Context, sourceURL string) ([]chapters.Chapter, error) {
	raw, err := s.scraper.GetChapters(ctx, sourceURL)
	if err != nil {
		return nil, fmt.Errorf("fetch chapters: %w", err)
	}

	out := make([]chapters.Chapter, len(raw))
	for i, ch := range raw {
		out[i] = chapters.Chapter{Chapter: ch}
	}

	return out, nil
}

// SelectChapters filters a chapter list according to a selection request.
func (s *DownloadService) SelectChapters(
	all []chapters.Chapter,
	selection ChapterSelection,
) ([]chapters.Chapter, error) {
	selected, err := chapters.Filter(
		all,
		selection.Chapter,
		selection.Range,
		selection.ExcludeRange,
		selection.List,
		selection.ExcludeList,
	)
	if err != nil {
		return nil, fmt.Errorf("select chapters: %w", err)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no chapters selected")
	}

	return selected, nil
}

// FetchImages fetches image URLs for a chapter.
func (s *DownloadService) FetchImages(ctx context.Context, chapter chapters.Chapter) ([]string, error) {
	images, err := s.scraper.GetImages(ctx, chapter.URL)
	if err != nil {
		return nil, fmt.Errorf("fetch images for %s: %w", chapter.Label, err)
	}

	return images, nil
}

// Download downloads selected chapters and writes CBZ files.
func (s *DownloadService) Download(
	ctx context.Context,
	selected []chapters.Chapter,
) (DownloadSummary, error) {
	if len(selected) == 0 {
		return DownloadSummary{}, fmt.Errorf("no chapters selected")
	}

	start := time.Now()
	dl := downloader.New(s.client, s.cfg.Debug, s.cfg.Output, s.cfg.SkipBroken)

	var summary downloadCounters
	sem := make(chan struct{}, max(1, s.cfg.ChapterWorkers))
	var wg sync.WaitGroup

schedule:
	for _, ch := range selected {
		select {
		case <-ctx.Done():
			break schedule
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(ch chapters.Chapter) {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := s.downloadChapter(ctx, dl, ch)
			if err != nil {
				msg := fmt.Sprintf("chapter %s: %v", ch.Label, err)
				s.log.Errorf("%s\n", msg)
				summary.addError(msg)
				summary.failedChapters.Add(1)
				return
			}
			summary.chapters.Add(1)
			summary.images.Add(int64(result.Images))
			summary.bytes.Add(result.Bytes)
		}(ch)
	}

	wg.Wait()
	if s.progress != nil {
		s.progress.Close()
	}

	if err := ctx.Err(); err != nil {
		return summary.toSummary(time.Since(start)), fmt.Errorf("download cancelled: %w", err)
	}

	return summary.toSummary(time.Since(start)), nil
}

// DownloadChapter downloads one chapter and writes its CBZ file.
func (s *DownloadService) DownloadChapter(ctx context.Context, ch chapters.Chapter) (ChapterDownloadResult, error) {
	return s.downloadChapter(ctx, downloader.New(s.client, s.cfg.Debug, s.cfg.Output, s.cfg.SkipBroken), ch)
}

func (s *DownloadService) downloadChapter(
	ctx context.Context,
	dl *downloader.Downloader,
	ch chapters.Chapter,
) (ChapterDownloadResult, error) {
	images, err := s.scraper.GetImages(ctx, ch.URL)
	if err != nil {
		return ChapterDownloadResult{}, fmt.Errorf("fetch images for %s: %w", ch.Label, err)
	}
	if len(images) == 0 {
		return ChapterDownloadResult{}, fmt.Errorf("no images for %s", ch.Label)
	}

	handle := s.progressHandle("Ch." + ch.Label)
	handle.SetTotal(len(images))

	tmpFolder := filepath.Join(s.cfg.Output, ch.FolderName())
	cbzOut := filepath.Join(s.cfg.Output, ch.OutputCBZ())

	files, bytes, err := dl.DownloadImagesConcurrently(ctx, images, tmpFolder, ch.URL, max(1, s.cfg.ImageWorkers), handle)
	if err != nil {
		return ChapterDownloadResult{}, err
	}

	if err := util.CreateCBZ(files, cbzOut); err != nil {
		return ChapterDownloadResult{}, fmt.Errorf("create cbz: %w", err)
	}

	if !s.cfg.KeepFolders {
		util.CleanupFolder(tmpFolder)
	}

	handle.MarkDone()
	return ChapterDownloadResult{Chapter: ch, OutputFile: cbzOut, Images: len(files), Bytes: bytes}, nil
}

func (s *DownloadService) progressHandle(prefix string) ProgressHandle {
	if s.progress == nil {
		return noopProgressHandle{}
	}

	return s.progress.Register(prefix)
}

type downloadCounters struct {
	chapters       atomic.Int64
	images         atomic.Int64
	bytes          atomic.Int64
	failedChapters atomic.Int64
	mu             sync.Mutex
	errors         []string
}

func (c *downloadCounters) toSummary(duration time.Duration) DownloadSummary {
	c.mu.Lock()
	errors := append([]string(nil), c.errors...)
	c.mu.Unlock()
	return DownloadSummary{
		Chapters:       c.chapters.Load(),
		Images:         c.images.Load(),
		Bytes:          c.bytes.Load(),
		FailedChapters: c.failedChapters.Load(),
		Errors:         errors,
		Duration:       duration,
	}
}

func (c *downloadCounters) addError(msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, msg)
}

type noopProgressHandle struct{}

func (noopProgressHandle) SetTotal(_ int) {}

func (noopProgressHandle) Update(_, _ int, _ int64) {}

func (noopProgressHandle) MarkDone() {}

type noopLogger struct{}

func (noopLogger) Debugf(_ string, _ ...any) {}

func (noopLogger) Errorf(_ string, _ ...any) {}
