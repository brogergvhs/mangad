package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brogergvhs/mangad/internal/browserdownload"
	"github.com/brogergvhs/mangad/internal/browserfetch"
	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/ui"
)

// JobService enqueues and runs jobs.
type JobService struct {
	db         *sql.DB
	dbPath     string
	jobTimeout time.Duration
	runtime    func() (*config.Config, ui.Log, error)
	jobs       *jobs.Repository
	lib        *LibraryService
	src        *sourceService
	want       *WantedService
	running    sync.Map // job ID -> context.CancelCauseFunc for in-flight jobs
}

// errJobCancelled aborts an in-flight job on explicit user cancellation.
var errJobCancelled = errors.New("cancelled by user")

// JobPayload is the common payload for background jobs.
type JobPayload struct {
	TitleID     int64  `json:"title_id,omitempty"`
	SourceID    string `json:"source_id,omitempty"`
	CatalogID   int64  `json:"catalog_id,omitempty"`
	ResetFailed bool   `json:"reset_failed,omitempty"`
}

// RunSummary describes one queue drain.
type RunSummary struct {
	Done   int
	Failed int
}

const (
	SettingServeRefreshEvery  = "serve.refresh_every"
	SettingServeScanEvery     = "serve.scan_every"
	SettingServeDownloadEvery = "serve.download_every"
	SettingServeRunEvery      = "serve.run_every"

	SettingBrowserSolverEnabled            = "browser_solver.enabled"
	SettingBrowserSolverProvider           = "browser_solver.provider"
	SettingBrowserSolverEndpoint           = "browser_solver.endpoint"
	SettingBrowserSolverTimeoutSeconds     = "browser_solver.timeout_seconds"
	SettingBrowserDownloaderEnabled        = "browser_downloader.enabled"
	SettingBrowserDownloaderEndpoint       = "browser_downloader.endpoint"
	SettingBrowserDownloaderTimeoutSeconds = "browser_downloader.timeout_seconds"
	SettingSourceRegistryURL               = "sources.registry_url"

	SettingJobsMaxAttempts      = "jobs.max_attempts"
	SettingJobsTimeout          = "jobs.timeout"
	SettingDownloadsMaxAttempts = "downloads.max_attempts"

	SettingServicesHealthInterval = "services.health_interval"

	defaultJobTimeout = 10 * time.Minute
)

// SettingDefault returns the built-in value for an app setting.
func SettingDefault(key string) string {
	switch key {
	case SettingServeRefreshEvery:
		return "1h"
	case SettingServeScanEvery:
		return "30m"
	case SettingServeDownloadEvery:
		return "10m"
	case SettingServeRunEvery:
		return "5s"
	case SettingBrowserSolverEnabled:
		return "false"
	case SettingBrowserSolverProvider:
		return browserfetch.ProviderFlareSolverr
	case SettingBrowserSolverEndpoint:
		return browserfetch.DefaultFlareSolverrEndpoint
	case SettingBrowserSolverTimeoutSeconds:
		return "60"
	case SettingBrowserDownloaderEnabled:
		return "false"
	case SettingBrowserDownloaderEndpoint:
		return browserdownload.DefaultEndpoint
	case SettingBrowserDownloaderTimeoutSeconds:
		return "180"
	case SettingSourceRegistryURL:
		return ""
	case SettingJobsMaxAttempts, SettingDownloadsMaxAttempts:
		return "3"
	case SettingJobsTimeout:
		return "10m"
	case SettingServicesHealthInterval:
		return "60s"
	default:
		return ""
	}
}

// SettingKeys returns settings exposed through the API.
func SettingKeys() []string {
	return []string{
		SettingServeRefreshEvery,
		SettingServeScanEvery,
		SettingServeDownloadEvery,
		SettingServeRunEvery,
		SettingBrowserSolverEnabled,
		SettingBrowserSolverProvider,
		SettingBrowserSolverEndpoint,
		SettingBrowserSolverTimeoutSeconds,
		SettingBrowserDownloaderEnabled,
		SettingBrowserDownloaderEndpoint,
		SettingBrowserDownloaderTimeoutSeconds,
		SettingSourceRegistryURL,
		SettingJobsMaxAttempts,
		SettingJobsTimeout,
		SettingDownloadsMaxAttempts,
		SettingServicesHealthInterval,
	}
}

// ValidateSetting checks an app setting update.
func ValidateSetting(key, value string) error {
	if !isSettingKey(key) {
		return fmt.Errorf("unknown setting %q", key)
	}

	switch key {
	case SettingServeRefreshEvery, SettingServeScanEvery, SettingServeDownloadEvery, SettingServeRunEvery, SettingServicesHealthInterval:
		return validateDurationSetting(key, value)
	case SettingBrowserSolverEnabled, SettingBrowserDownloaderEnabled:
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("invalid bool for %s", key)
		}
	case SettingBrowserSolverProvider:
		if value != browserfetch.ProviderFlareSolverr {
			return fmt.Errorf("unsupported provider %q", value)
		}
	case SettingBrowserSolverEndpoint, SettingBrowserDownloaderEndpoint:
		u, err := url.ParseRequestURI(value)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("invalid endpoint for %s", key)
		}
	case SettingBrowserSolverTimeoutSeconds, SettingBrowserDownloaderTimeoutSeconds:
		seconds, err := strconv.Atoi(value)
		if err != nil || seconds <= 0 {
			return fmt.Errorf("invalid timeout seconds for %s", key)
		}
	case SettingSourceRegistryURL:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		u, err := url.ParseRequestURI(value)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("invalid registry url for %s", key)
		}
	case SettingJobsMaxAttempts, SettingDownloadsMaxAttempts:
		if n, err := strconv.Atoi(value); err != nil || n <= 0 {
			return fmt.Errorf("invalid positive integer for %s", key)
		}
	case SettingJobsTimeout:
		if d, err := time.ParseDuration(value); err != nil || d <= 0 {
			return fmt.Errorf("invalid positive duration for %s", key)
		}
	}
	return nil
}

func isSettingKey(key string) bool {
	for _, known := range SettingKeys() {
		if key == known {
			return true
		}
	}
	return false
}

func validateDurationSetting(key, value string) error {
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 {
		return fmt.Errorf("invalid duration for %s", key)
	}
	if key == SettingServeRunEvery && d == 0 {
		return fmt.Errorf("%s cannot be 0", key)
	}
	return nil
}

// OpenJobs opens the app database for job processing.
func OpenJobs(ctx context.Context, dbPath string) (*JobService, func(), error) {
	if dbPath == "" {
		dbPath = database.DefaultPath()
	}
	db, err := database.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, err
	}
	if err := database.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, nil, err
	}

	svc := newJobService(db)
	svc.dbPath = dbPath
	if _, err := svc.lib.ReconcileStartedDownloads(ctx); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	if _, err := svc.jobs.ReconcileRunning(ctx); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	svc.applyLimits(ctx)
	go func() {
		if _, err := svc.lib.ScanDownloads(ctx, 0); err != nil && ctx.Err() == nil {
			log.Printf("startup download stats scan failed: %v", err)
		}
	}()
	return svc, func() { _ = db.Close() }, nil
}

func newJobService(db *sql.DB) *JobService {
	return &JobService{
		db:         db,
		dbPath:     database.DefaultPath(),
		jobTimeout: defaultJobTimeout,
		runtime: func() (*config.Config, ui.Log, error) {
			return config.DefaultConfig(), ui.NewLogger(false), nil
		},
		jobs: jobs.NewRepository(db),
		lib:  newLibraryService(db),
		src:  newSourceService(db),
		want: newWantedService(db),
	}
}

// applyLimits seeds the retry/timeout tunables from stored settings.
func (s *JobService) applyLimits(ctx context.Context) {
	if n, err := strconv.Atoi(s.Setting(ctx, SettingJobsMaxAttempts, SettingDefault(SettingJobsMaxAttempts))); err == nil && n > 0 {
		s.jobs.MaxAttempts = n
	}
	if n, err := strconv.Atoi(s.Setting(ctx, SettingDownloadsMaxAttempts, SettingDefault(SettingDownloadsMaxAttempts))); err == nil && n > 0 {
		s.lib.repo.MaxDownloadAttempts = n
	}
	if d, err := time.ParseDuration(s.Setting(ctx, SettingJobsTimeout, SettingDefault(SettingJobsTimeout))); err == nil && d > 0 {
		s.jobTimeout = d
	}
}

// SetRuntimeConfig overrides how job execution loads the base runtime config
// (e.g. the CLI's merged config file); DB-backed settings still overlay it.
func (s *JobService) SetRuntimeConfig(fn func() (*config.Config, ui.Log, error)) {
	if fn != nil {
		s.runtime = fn
	}
}

// RuntimeConfig returns the merged runtime config for job execution.
func (s *JobService) RuntimeConfig(ctx context.Context) (*config.Config, ui.Log, error) {
	cfg, logSvc, err := s.runtime()
	if err != nil {
		return nil, nil, err
	}
	s.ApplySettings(ctx, cfg)
	cfg.CookieDBPath = s.dbPath
	return cfg, logSvc, nil
}

// Setting returns an app setting, or fallback when the key is unset.
func (s *JobService) Setting(ctx context.Context, key, fallback string) string {
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("read setting %s: %v (using fallback)", key, err)
		}
		return fallback
	}
	return value
}

// SetSetting stores an app setting.
func (s *JobService) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// ClearSetting removes a stored override so the config/env default applies.
func (s *JobService) ClearSetting(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return fmt.Errorf("clear setting %s: %w", key, err)
	}
	return nil
}

// ApplySettings overlays DB-backed app settings onto cfg.
func (s *JobService) ApplySettings(ctx context.Context, cfg *config.Config) {
	if value := s.Setting(ctx, SettingBrowserSolverEnabled, ""); value != "" {
		if enabled, err := strconv.ParseBool(value); err == nil {
			cfg.BrowserSolver.Enabled = enabled
		}
	}
	if value := s.Setting(ctx, SettingBrowserSolverProvider, ""); value != "" {
		cfg.BrowserSolver.Provider = value
	}
	if value := s.Setting(ctx, SettingBrowserSolverEndpoint, ""); value != "" {
		cfg.BrowserSolver.Endpoint = value
	}
	if value := s.Setting(ctx, SettingBrowserSolverTimeoutSeconds, ""); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil {
			cfg.BrowserSolver.TimeoutSeconds = seconds
		}
	}
	if value := s.Setting(ctx, SettingBrowserDownloaderEnabled, ""); value != "" {
		if enabled, err := strconv.ParseBool(value); err == nil {
			cfg.BrowserDownload.Enabled = enabled
		}
	}
	if value := s.Setting(ctx, SettingBrowserDownloaderEndpoint, ""); value != "" {
		cfg.BrowserDownload.Endpoint = value
	}
	if value := s.Setting(ctx, SettingBrowserDownloaderTimeoutSeconds, ""); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil {
			cfg.BrowserDownload.TimeoutSeconds = seconds
		}
	}
}

// BrowserSolverHealth checks the configured browser solver endpoint.
func (s *JobService) BrowserSolverHealth(ctx context.Context) (bool, error) {
	enabled, _ := strconv.ParseBool(s.Setting(ctx, SettingBrowserSolverEnabled, SettingDefault(SettingBrowserSolverEnabled)))
	if !enabled {
		return false, nil
	}
	if provider := s.Setting(ctx, SettingBrowserSolverProvider, SettingDefault(SettingBrowserSolverProvider)); provider != browserfetch.ProviderFlareSolverr {
		return false, fmt.Errorf("unsupported browser solver provider %q", provider)
	}

	timeoutSeconds, _ := strconv.Atoi(s.Setting(ctx, SettingBrowserSolverTimeoutSeconds, SettingDefault(SettingBrowserSolverTimeoutSeconds)))
	client := browserfetch.NewFlareSolverr(
		s.Setting(ctx, SettingBrowserSolverEndpoint, SettingDefault(SettingBrowserSolverEndpoint)),
		time.Duration(timeoutSeconds)*time.Second,
		nil,
	)
	if err := client.Health(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// ListTitles returns tracked titles.
func (s *JobService) ListTitles(ctx context.Context) ([]library.Title, error) {
	return s.lib.ListTitles(ctx)
}

// GetTitle returns one tracked title.
func (s *JobService) GetTitle(ctx context.Context, id int64) (library.Title, error) {
	return s.lib.GetTitle(ctx, id)
}

// TitleChapters returns all discovered chapters for a title with download state.
func (s *JobService) TitleChapters(ctx context.Context, id int64) ([]library.ChapterStatus, error) {
	return s.lib.ListChapters(ctx, id)
}

// ListTitleSources returns all sources linked to a title.
func (s *JobService) ListTitleSources(ctx context.Context, id int64) ([]library.LinkedSource, error) {
	return s.lib.ListTitleSources(ctx, id)
}

// UnlinkTitleSource removes a linked source from a title.
func (s *JobService) UnlinkTitleSource(ctx context.Context, id int64, url string) error {
	return s.lib.UnlinkSource(ctx, id, url)
}

// AddCatalogTitle adds an AniList manga to the library as a source-less title.
func (s *JobService) AddCatalogTitle(ctx context.Context, anilistID int) (library.Title, error) {
	title, err := s.want.AddCatalogTitle(ctx, anilistID)
	if err != nil {
		return library.Title{}, err
	}
	if err := s.enqueueSourceSearchForTitle(ctx, title); err != nil {
		return title, err
	}
	return title, nil
}

// TitlesByProvider maps a catalog provider's manga IDs to tracked title IDs.
func (s *JobService) TitlesByProvider(ctx context.Context, provider string) (map[string]int64, error) {
	return s.lib.TitlesByProvider(ctx, provider)
}

// GetManga returns stored catalog metadata for a manga.
func (s *JobService) GetManga(ctx context.Context, catalogID int64) (catalog.Manga, error) {
	return s.want.GetManga(ctx, catalogID)
}

// RemoveTitle removes a tracked title.
func (s *JobService) RemoveTitle(ctx context.Context, id int64) (library.Title, error) {
	return s.lib.RemoveTitle(ctx, id)
}

// SetMonitored toggles monitoring for a tracked title.
// SetRefreshInterval sets a title's custom refresh cadence (empty = global).
func (s *JobService) SetRefreshInterval(ctx context.Context, id int64, interval string) error {
	return s.lib.SetRefreshInterval(ctx, id, interval)
}

func (s *JobService) SetMonitored(ctx context.Context, id int64, monitored bool) error {
	return s.lib.SetMonitored(ctx, id, monitored)
}

// SearchAniList searches AniList and stores returned metadata locally.
func (s *JobService) SearchAniList(ctx context.Context, query string, limit int) ([]catalog.Manga, error) {
	return s.want.SearchAniList(ctx, query, limit)
}

// AddAniListWanted adds an AniList title to wanted.
func (s *JobService) AddAniListWanted(ctx context.Context, anilistID int) (catalog.Manga, error) {
	return s.want.AddAniListWanted(ctx, anilistID)
}

// ListWanted returns wanted canonical titles.
func (s *JobService) ListWanted(ctx context.Context) ([]catalog.Manga, error) {
	return s.want.ListWanted(ctx)
}

// ExploreDownloads lists untracked folders in the download directory.
func (s *JobService) ExploreDownloads(ctx context.Context) ([]ImportCandidate, error) {
	cfg, _, err := s.RuntimeConfig(ctx)
	if err != nil {
		return nil, err
	}
	return s.want.ExploreDownloads(ctx, cfg.DownloadDir)
}

// ImportFolder tracks an existing download folder against an AniList entry.
func (s *JobService) ImportFolder(ctx context.Context, folder string, anilistID int) (library.Title, error) {
	cfg, _, err := s.RuntimeConfig(ctx)
	if err != nil {
		return library.Title{}, err
	}
	title, err := s.want.ImportFolder(ctx, cfg.DownloadDir, folder, anilistID)
	if err != nil {
		return library.Title{}, err
	}
	if err := s.enqueueSourceSearchForTitle(ctx, title); err != nil {
		return title, err
	}
	return title, nil
}

// LinkTitleSource links a tracked title to a matched source.
func (s *JobService) LinkTitleSource(ctx context.Context, titleID, matchID int64) (library.Title, error) {
	title, err := s.want.LinkTitleSource(ctx, titleID, matchID)
	if err != nil {
		return library.Title{}, err
	}
	if err := s.enqueueRefreshForTitle(ctx, title); err != nil {
		return title, err
	}
	return title, nil
}

// MatchSources finds source matches for one canonical title.
func (s *JobService) MatchSources(ctx context.Context, catalogID int64) ([]catalog.Match, error) {
	cfg, logSvc, err := s.RuntimeConfig(ctx)
	if err != nil {
		return nil, err
	}
	return s.want.MatchSources(ctx, cfg, logSvc, catalogID)
}

// ListMatches returns persisted source matches.
func (s *JobService) ListMatches(ctx context.Context, catalogID int64) ([]catalog.Match, error) {
	return s.want.ListMatches(ctx, catalogID)
}

// TrackMatch adds a selected match to the tracked library.
func (s *JobService) TrackMatch(ctx context.Context, matchID int64, output string, monitored bool, refreshInterval string) (library.Title, error) {
	title, err := s.want.TrackMatch(ctx, matchID, output, monitored, refreshInterval)
	if err != nil {
		return library.Title{}, err
	}
	if err := s.enqueueRefreshForTitle(ctx, title); err != nil {
		return title, err
	}
	return title, nil
}

// TrackMatchDefault adds a selected match using catalog status for monitoring.
func (s *JobService) TrackMatchDefault(ctx context.Context, matchID int64, output string, refreshInterval string) (library.Title, error) {
	match, err := s.want.catalog.GetMatch(ctx, matchID)
	if err != nil {
		return library.Title{}, err
	}
	manga, err := s.want.catalog.GetManga(ctx, match.CatalogMangaID)
	if err != nil {
		return library.Title{}, err
	}
	return s.TrackMatch(ctx, matchID, output, defaultMonitored(manga), refreshInterval)
}

// SyncSources stores bundled profiles and, when set, a remote registry.
func (s *JobService) SyncSources(ctx context.Context, registryURL string) error {
	if err := s.src.SyncBuiltIn(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(registryURL) == "" {
		return nil
	}
	return s.src.SyncRegistry(ctx, registryURL)
}

// ListSources returns known source profiles.
func (s *JobService) ListSources(ctx context.Context) ([]sources.Source, error) {
	return s.src.ListSources(ctx)
}

// GetSource returns one source profile.
func (s *JobService) GetSource(ctx context.Context, id string) (sources.Source, error) {
	return s.src.GetSource(ctx, id)
}

// ImportLocalSource stores a local DB-backed source profile.
func (s *JobService) ImportLocalSource(ctx context.Context, profile sources.Profile) error {
	if err := s.src.ImportLocal(ctx, profile); err != nil {
		return err
	}
	return s.enqueueRefreshForSource(ctx, profile.ID)
}

// RemoveLocalSource removes a local DB-backed source profile.
func (s *JobService) RemoveLocalSource(ctx context.Context, sourceID string) error {
	return s.src.RemoveLocal(ctx, sourceID)
}

// ExportSource returns one source profile as YAML.
func (s *JobService) ExportSource(ctx context.Context, sourceID string) ([]byte, error) {
	return s.src.ExportSource(ctx, sourceID)
}

// VerifySource checks one source profile.
func (s *JobService) VerifySource(ctx context.Context, cfg *config.Config, logSvc ui.Log, sourceID string) (SourceVerifyResult, error) {
	return s.src.VerifySource(ctx, cfg, logSvc, sourceID)
}

// VerifySourceURL probes a user-specified manga URL within a chosen source,
// reporting how many chapters it finds so the user can confirm before linking.
func (s *JobService) VerifySourceURL(ctx context.Context, sourceID, rawURL string) (SourceTestResult, error) {
	src, err := s.sourceForURL(ctx, sourceID, rawURL)
	if err != nil {
		return SourceTestResult{}, err
	}
	cfg, logSvc, err := s.RuntimeConfig(ctx)
	if err != nil {
		return SourceTestResult{}, err
	}
	profile := src.Profile
	profile.SampleMangaURL = strings.TrimSpace(rawURL)
	useSolver := src.RequiresBrowserSolver || src.ChapterFetch == sources.FetchSolver
	useBrowser := src.RequiresBrowserDownload || src.ImageFetch == sources.FetchBrowser
	return s.src.TestProfile(ctx, cfg, logSvc, profile, useSolver, useBrowser), nil
}

// LinkTitleSourceURL links a title to a user-specified URL served by a chosen
// source, so its scraper and fetch methods apply.
func (s *JobService) LinkTitleSourceURL(ctx context.Context, titleID int64, sourceID, rawURL string) (library.Title, error) {
	src, err := s.sourceForURL(ctx, sourceID, rawURL)
	if err != nil {
		return library.Title{}, err
	}
	title, err := s.want.LinkTitleURL(ctx, titleID, strings.TrimSpace(rawURL), src.ID)
	if err != nil {
		return library.Title{}, err
	}
	if err := s.enqueueRefreshForTitle(ctx, title); err != nil {
		return title, err
	}
	return title, nil
}

// sourceForURL validates that rawURL is a well-formed page on the chosen source.
func (s *JobService) sourceForURL(ctx context.Context, sourceID, rawURL string) (sources.Source, error) {
	rawURL = strings.TrimSpace(rawURL)
	if u, err := url.ParseRequestURI(rawURL); err != nil || !strings.HasPrefix(u.Scheme, "http") {
		return sources.Source{}, fmt.Errorf("enter a full http(s) URL")
	}
	src, err := s.src.GetSource(ctx, sourceID)
	if err != nil {
		return sources.Source{}, err
	}
	if _, ok := MatchSourceForURL([]sources.Source{src}, rawURL); !ok {
		return sources.Source{}, fmt.Errorf("that URL is not on %s", src.Name)
	}
	return src, nil
}

// LinkTitleToSource links a title to a registered source by ID, using that
// source's manga URL. Its fetch methods and image extensions then apply.
func (s *JobService) LinkTitleToSource(ctx context.Context, titleID int64, sourceID string) (library.Title, error) {
	src, err := s.src.GetSource(ctx, sourceID)
	if err != nil {
		return library.Title{}, err
	}
	if _, err := url.ParseRequestURI(src.SampleMangaURL); err != nil {
		return library.Title{}, fmt.Errorf("source %q has no manga URL to link", src.Name)
	}
	title, err := s.want.LinkTitleURL(ctx, titleID, src.SampleMangaURL, src.ID)
	if err != nil {
		return library.Title{}, err
	}
	if err := s.enqueueRefreshForTitle(ctx, title); err != nil {
		return title, err
	}
	return title, nil
}

// ServiceHealth is the reachability of one external helper service.
type ServiceHealth struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Enabled  bool   `json:"enabled"`
	Healthy  bool   `json:"healthy"`
	Error    string `json:"error,omitempty"`
}

// ServicesHealth checks the configured FlareSolverr solver and browser
// downloader, reporting each one's reachability.
func (s *JobService) ServicesHealth(ctx context.Context) []ServiceHealth {
	cfg, _, err := s.RuntimeConfig(ctx)
	if err != nil || cfg == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	solver := ServiceHealth{Name: "FlareSolverr (page solver)", Endpoint: cfg.BrowserSolver.Endpoint, Enabled: cfg.BrowserSolver.Enabled}
	if solver.Enabled {
		client := browserfetch.NewFlareSolverr(cfg.BrowserSolver.Endpoint, time.Duration(cfg.BrowserSolver.TimeoutSeconds)*time.Second, nil)
		if err := client.Health(ctx); err != nil {
			solver.Error = err.Error()
		} else {
			solver.Healthy = true
		}
	}

	browser := ServiceHealth{Name: "Browser downloader (Selenium)", Endpoint: cfg.BrowserDownload.Endpoint, Enabled: cfg.BrowserDownload.Enabled}
	if browser.Enabled {
		client := browserdownload.New(cfg.BrowserDownload.Endpoint, 8*time.Second, nil)
		if err := client.Health(ctx); err != nil {
			browser.Error = err.Error()
		} else {
			browser.Healthy = true
		}
	}

	return []ServiceHealth{solver, browser}
}

// SetSourceMethods overrides a source's chapter/image fetch methods.
func (s *JobService) SetSourceMethods(ctx context.Context, sourceID, chapterFetch, imageFetch string) error {
	if err := s.src.SetFetchMethods(ctx, sourceID, chapterFetch, imageFetch); err != nil {
		return err
	}
	return s.enqueueRefreshForSource(ctx, sourceID)
}

func (s *JobService) enqueueSourceSearchForTitle(ctx context.Context, title library.Title) error {
	if title.CatalogMangaID == nil {
		return nil
	}
	if _, err := s.EnqueueCatalog(ctx, jobs.TypeMatchSources, *title.CatalogMangaID, time.Now()); err != nil {
		return fmt.Errorf("queue source search: %w", err)
	}
	return nil
}

func (s *JobService) enqueueRefreshForTitle(ctx context.Context, title library.Title) error {
	if !strings.HasPrefix(strings.TrimSpace(title.SourceURL), "http") {
		return nil
	}
	if _, err := s.Enqueue(ctx, jobs.TypeRefreshTitle, title.ID, time.Now()); err != nil {
		return fmt.Errorf("queue chapter refresh: %w", err)
	}
	return nil
}

func (s *JobService) enqueueRefreshForSource(ctx context.Context, sourceID string) error {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil
	}
	titles, err := s.lib.ListTitles(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, title := range titles {
		if title.SourceID != sourceID {
			continue
		}
		errs = append(errs, s.enqueueRefreshForTitle(ctx, title))
	}
	return errors.Join(errs...)
}

// TestSource probes a candidate source profile with the chosen fetch methods.
func (s *JobService) TestSource(ctx context.Context, profile sources.Profile, useSolver, useBrowser bool) (SourceTestResult, error) {
	cfg, logSvc, err := s.RuntimeConfig(ctx)
	if err != nil {
		return SourceTestResult{}, err
	}
	return s.src.TestProfile(ctx, cfg, logSvc, profile, useSolver, useBrowser), nil
}

// Enqueue creates a job.
func (s *JobService) Enqueue(ctx context.Context, typ string, titleID int64, runAfter time.Time) (jobs.Job, error) {
	payload := JobPayload{TitleID: titleID}
	if typ == jobs.TypeDownloadMissing && titleID > 0 {
		payload.ResetFailed = true
	}
	return s.enqueue(ctx, typ, payload, runAfter)
}

// EnqueueSource creates a source-scoped job.
func (s *JobService) EnqueueSource(ctx context.Context, sourceID string, runAfter time.Time) (jobs.Job, error) {
	return s.enqueue(ctx, jobs.TypeVerifySource, JobPayload{SourceID: strings.TrimSpace(sourceID)}, runAfter)
}

// EnqueueCatalog creates a catalog-scoped job.
func (s *JobService) EnqueueCatalog(ctx context.Context, typ string, catalogID int64, runAfter time.Time) (jobs.Job, error) {
	return s.enqueue(ctx, typ, JobPayload{CatalogID: catalogID}, runAfter)
}

func (s *JobService) enqueue(ctx context.Context, typ string, payload JobPayload, runAfter time.Time) (jobs.Job, error) {
	if err := validateJob(typ, payload); err != nil {
		return jobs.Job{}, err
	}
	if job, ok, err := s.coveringJob(ctx, typ, payload); err != nil {
		return jobs.Job{}, err
	} else if ok {
		return job, nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return jobs.Job{}, fmt.Errorf("encode job payload: %w", err)
	}

	return s.jobs.Enqueue(ctx, typ, string(data), runAfter)
}

func (s *JobService) coveringJob(ctx context.Context, typ string, payload JobPayload) (jobs.Job, bool, error) {
	if payload.TitleID <= 0 || !titleScopedJob(typ) {
		return jobs.Job{}, false, nil
	}
	all, err := s.jobs.List(ctx)
	if err != nil {
		return jobs.Job{}, false, err
	}
	for _, job := range all {
		if job.Type != typ {
			continue
		}
		if !activeJobStatus(job.Status) {
			continue
		}
		var existing JobPayload
		if json.Unmarshal([]byte(job.Payload), &existing) != nil {
			continue
		}
		if existing.TitleID == 0 || existing.TitleID == payload.TitleID {
			return job, true, nil
		}
	}
	return jobs.Job{}, false, nil
}

// CancelJob aborts a job: a running job's context is cancelled; a queued or
// awaiting-retry job is marked cancelled so it never runs.
func (s *JobService) CancelJob(ctx context.Context, id int64) error {
	if v, ok := s.running.Load(id); ok {
		v.(context.CancelCauseFunc)(errJobCancelled)
		return nil
	}
	if _, err := s.jobs.Cancel(ctx, id); err != nil {
		return err
	}
	return nil
}

// List returns recent jobs.
func (s *JobService) List(ctx context.Context) ([]jobs.Job, error) {
	return s.jobs.List(ctx)
}

// RunDue claims and runs due jobs until the queue is empty.
func (s *JobService) RunDue(ctx context.Context, cfg *config.Config, logSvc ui.Log) (RunSummary, error) {
	var summary RunSummary
	// Outcomes must be persisted even when ctx is cancelled mid-job;
	// marking with the cancelled ctx would strand the job as running.
	markCtx := context.WithoutCancel(ctx)
	for {
		job, ok, err := s.jobs.ClaimNext(ctx)
		if err != nil || !ok {
			return summary, err
		}

		// A user-cancellable layer wraps the inactivity-timeout layer. The
		// inactivity timeout lets a job that keeps making progress run as long
		// as it needs; only stalled or explicitly cancelled jobs abort.
		userCtx, userCancel := context.WithCancelCause(ctx)
		jobCtx, guard, stopStall := stallContext(userCtx, s.jobTimeout)
		s.running.Store(job.ID, userCancel)
		err = s.run(jobCtx, cfg, logSvc, job, guard)
		s.running.Delete(job.ID)
		cancelled := errors.Is(context.Cause(userCtx), errJobCancelled)
		if err != nil && !cancelled {
			if cause := context.Cause(jobCtx); cause != nil && !errors.Is(cause, context.Canceled) {
				err = fmt.Errorf("%v: %w", cause, err)
			}
		}
		stopStall()
		userCancel(nil)
		if cancelled {
			summary.Failed++
			if markErr := s.jobs.MarkCancelled(markCtx, job.ID); markErr != nil {
				return summary, markErr
			}
			continue
		}
		if err != nil {
			summary.Failed++
			if markErr := s.jobs.MarkFailed(markCtx, job.ID, err); markErr != nil {
				return summary, markErr
			}
			continue
		}
		summary.Done++
		if err := s.jobs.MarkDone(markCtx, job.ID); err != nil {
			return summary, err
		}
	}
}

func (s *JobService) run(ctx context.Context, cfg *config.Config, logSvc ui.Log, job jobs.Job, progress ProgressManager) error {
	var payload JobPayload
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		return fmt.Errorf("decode job payload: %w", err)
	}

	switch job.Type {
	case jobs.TypeRefreshTitle:
		if payload.TitleID > 0 {
			title, err := s.lib.GetTitle(ctx, payload.TitleID)
			if err != nil {
				return err
			}
			_, err = s.lib.RefreshTitle(ctx, cfg, logSvc, title)
			return err
		}
		return s.expandTitleJob(ctx, jobs.TypeRefreshTitle)
	case jobs.TypeScanDownloads:
		if payload.TitleID > 0 {
			_, err := s.lib.ScanDownloads(ctx, payload.TitleID)
			return err
		}
		return s.expandTitleJob(ctx, jobs.TypeScanDownloads)
	case jobs.TypeDownloadMissing:
		if payload.TitleID > 0 {
			if payload.ResetFailed {
				if err := s.lib.ResetFailedDownloads(ctx, payload.TitleID); err != nil {
					return err
				}
			}
			if _, err := s.lib.DownloadMissing(ctx, cfg, logSvc, payload.TitleID, progress); err != nil {
				return err
			}
			return nil
		}
		return s.expandTitleJob(ctx, jobs.TypeDownloadMissing)
	case jobs.TypeVerifySource:
		_, err := s.VerifySource(ctx, cfg, logSvc, payload.SourceID)
		return err
	case jobs.TypeMatchSources:
		_, err := s.want.MatchSources(ctx, cfg, logSvc, payload.CatalogID)
		return err
	default:
		return fmt.Errorf("unknown job type %q", job.Type)
	}
}

func (s *JobService) expandTitleJob(ctx context.Context, typ string) error {
	titles, err := s.lib.ListTitles(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	var errs []error
	for _, title := range titles {
		if !globalTitleJobApplies(typ, title, now) {
			continue
		}
		_, err := s.enqueue(ctx, typ, JobPayload{TitleID: title.ID}, time.Now())
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func titleRefreshDue(title library.Title, now time.Time) bool {
	interval := strings.TrimSpace(title.RefreshInterval)
	if interval == "" {
		return true // no override: follow the global sweep cadence
	}
	d, err := time.ParseDuration(interval)
	if err != nil || d <= 0 || title.LastRefreshedAt == nil {
		return true
	}
	return now.Sub(*title.LastRefreshedAt) >= d
}

func globalTitleJobApplies(typ string, title library.Title, now time.Time) bool {
	switch typ {
	case jobs.TypeRefreshTitle:
		return title.Monitored && titleRefreshDue(title, now)
	case jobs.TypeDownloadMissing:
		return title.Monitored && title.MissingCount > 0
	case jobs.TypeScanDownloads:
		return title.CompletedCount > 0
	default:
		return false
	}
}

func validateJob(typ string, payload JobPayload) error {
	switch typ {
	case jobs.TypeRefreshTitle, jobs.TypeScanDownloads, jobs.TypeDownloadMissing:
		if payload.TitleID < 0 {
			return fmt.Errorf("invalid title id %d", payload.TitleID)
		}
	case jobs.TypeVerifySource:
		if strings.TrimSpace(payload.SourceID) == "" {
			return fmt.Errorf("source id is required")
		}
	case jobs.TypeMatchSources:
		if payload.CatalogID <= 0 {
			return fmt.Errorf("catalog id is required")
		}
	default:
		return fmt.Errorf("unknown job type %q", typ)
	}

	return nil
}

func titleScopedJob(typ string) bool {
	switch typ {
	case jobs.TypeRefreshTitle, jobs.TypeScanDownloads, jobs.TypeDownloadMissing:
		return true
	default:
		return false
	}
}

func activeJobStatus(status string) bool {
	switch status {
	case "queued", "running", "failed":
		return true
	default:
		return false
	}
}
