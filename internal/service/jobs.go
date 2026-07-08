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
	"time"

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
}

// JobPayload is the common payload for background jobs.
type JobPayload struct {
	TitleID   int64  `json:"title_id,omitempty"`
	SourceID  string `json:"source_id,omitempty"`
	CatalogID int64  `json:"catalog_id,omitempty"`
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

	SettingBrowserSolverEnabled        = "browser_solver.enabled"
	SettingBrowserSolverProvider       = "browser_solver.provider"
	SettingBrowserSolverEndpoint       = "browser_solver.endpoint"
	SettingBrowserSolverTimeoutSeconds = "browser_solver.timeout_seconds"
	SettingSourceRegistryURL           = "sources.registry_url"

	SettingJobsMaxAttempts      = "jobs.max_attempts"
	SettingJobsTimeout          = "jobs.timeout"
	SettingDownloadsMaxAttempts = "downloads.max_attempts"

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
	case SettingSourceRegistryURL:
		return ""
	case SettingJobsMaxAttempts, SettingDownloadsMaxAttempts:
		return "3"
	case SettingJobsTimeout:
		return "10m"
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
		SettingSourceRegistryURL,
		SettingJobsMaxAttempts,
		SettingJobsTimeout,
		SettingDownloadsMaxAttempts,
	}
}

// ValidateSetting checks an app setting update.
func ValidateSetting(key, value string) error {
	if !isSettingKey(key) {
		return fmt.Errorf("unknown setting %q", key)
	}

	switch key {
	case SettingServeRefreshEvery, SettingServeScanEvery, SettingServeDownloadEvery, SettingServeRunEvery:
		return validateDurationSetting(key, value)
	case SettingBrowserSolverEnabled:
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("invalid bool for %s", key)
		}
	case SettingBrowserSolverProvider:
		if value != browserfetch.ProviderFlareSolverr {
			return fmt.Errorf("unsupported provider %q", value)
		}
	case SettingBrowserSolverEndpoint:
		u, err := url.ParseRequestURI(value)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("invalid endpoint for %s", key)
		}
	case SettingBrowserSolverTimeoutSeconds:
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

// AddCatalogTitle adds an AniList manga to the library as a source-less title.
func (s *JobService) AddCatalogTitle(ctx context.Context, anilistID int) (library.Title, error) {
	return s.want.AddCatalogTitle(ctx, anilistID)
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
	return s.want.ImportFolder(ctx, cfg.DownloadDir, folder, anilistID)
}

// LinkTitleSource links a tracked title to a matched source.
func (s *JobService) LinkTitleSource(ctx context.Context, titleID, matchID int64) (library.Title, error) {
	return s.want.LinkTitleSource(ctx, titleID, matchID)
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
	return s.want.TrackMatch(ctx, matchID, output, monitored, refreshInterval)
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
	return s.src.ImportLocal(ctx, profile)
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

// LinkTitleURL links a title directly to a chapter-list URL, resolving which
// registered source (built-in or custom) serves that site by domain.
func (s *JobService) LinkTitleURL(ctx context.Context, titleID int64, rawURL string) (library.Title, error) {
	rawURL = strings.TrimSpace(rawURL)
	if u, err := url.ParseRequestURI(rawURL); err != nil || !strings.HasPrefix(u.Scheme, "http") {
		return library.Title{}, fmt.Errorf("enter a full http(s) URL")
	}
	src, ok := ResolveSourceForURL(ctx, rawURL, s.dbPath, nil)
	if !ok {
		return library.Title{}, fmt.Errorf("no source is registered for that site — add it as a custom source first")
	}
	return s.want.LinkTitleURL(ctx, titleID, rawURL, src.ID)
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
	return s.enqueue(ctx, typ, JobPayload{TitleID: titleID}, runAfter)
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

	data, err := json.Marshal(payload)
	if err != nil {
		return jobs.Job{}, fmt.Errorf("encode job payload: %w", err)
	}

	return s.jobs.Enqueue(ctx, typ, string(data), runAfter)
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

		jobCtx, cancel := context.WithTimeout(ctx, s.jobTimeout)
		err = s.run(jobCtx, cfg, logSvc, job)
		cancel()
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

func (s *JobService) run(ctx context.Context, cfg *config.Config, logSvc ui.Log, job jobs.Job) error {
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
		_, err := s.lib.RefreshMonitored(ctx, cfg, logSvc)
		return err
	case jobs.TypeScanDownloads:
		_, err := s.lib.ScanDownloads(ctx, payload.TitleID)
		return err
	case jobs.TypeDownloadMissing:
		if payload.TitleID > 0 {
			_, err := s.lib.DownloadMissing(ctx, cfg, logSvc, payload.TitleID)
			return err
		}
		_, err := s.lib.DownloadMonitoredMissing(ctx, cfg, logSvc)
		return err
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
