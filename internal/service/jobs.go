package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/brogergvhs/mangad/internal/browserfetch"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/ui"
)

// JobService enqueues and runs jobs.
type JobService struct {
	db   *sql.DB
	jobs *jobs.Repository
	lib  *LibraryService
}

// JobPayload is the common payload for title-scoped jobs.
type JobPayload struct {
	TitleID int64 `json:"title_id,omitempty"`
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
	}
}

// ValidateSetting checks an app setting update.
func ValidateSetting(key, value string) error {
	if SettingDefault(key) == "" {
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
	}
	return nil
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
	db, err := database.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, err
	}
	if err := database.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, nil, err
	}

	return newJobService(db), func() { _ = db.Close() }, nil
}

func newJobService(db *sql.DB) *JobService {
	return &JobService{
		db:   db,
		jobs: jobs.NewRepository(db),
		lib:  &LibraryService{repo: library.NewRepository(db)},
	}
}

// Setting returns an app setting or fallback.
func (s *JobService) Setting(ctx context.Context, key, fallback string) string {
	var value string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
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

// Enqueue creates a job.
func (s *JobService) Enqueue(ctx context.Context, typ string, titleID int64, runAfter time.Time) (jobs.Job, error) {
	if err := validateJob(typ, titleID); err != nil {
		return jobs.Job{}, err
	}

	payload, err := json.Marshal(JobPayload{TitleID: titleID})
	if err != nil {
		return jobs.Job{}, fmt.Errorf("encode job payload: %w", err)
	}

	return s.jobs.Enqueue(ctx, typ, string(payload), runAfter)
}

// List returns recent jobs.
func (s *JobService) List(ctx context.Context) ([]jobs.Job, error) {
	return s.jobs.List(ctx)
}

// RunDue claims and runs due jobs until the queue is empty.
func (s *JobService) RunDue(ctx context.Context, cfg *config.Config, logSvc *ui.Logger) (RunSummary, error) {
	var summary RunSummary
	for {
		job, ok, err := s.jobs.ClaimNext(ctx)
		if err != nil || !ok {
			return summary, err
		}

		if err := s.run(ctx, cfg, logSvc, job); err != nil {
			summary.Failed++
			if markErr := s.jobs.MarkFailed(ctx, job.ID, err); markErr != nil {
				return summary, markErr
			}
			continue
		}
		summary.Done++
		if err := s.jobs.MarkDone(ctx, job.ID); err != nil {
			return summary, err
		}
	}
}

func (s *JobService) run(ctx context.Context, cfg *config.Config, logSvc *ui.Logger, job jobs.Job) error {
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
	default:
		return fmt.Errorf("unknown job type %q", job.Type)
	}
}

func validateJob(typ string, titleID int64) error {
	switch typ {
	case jobs.TypeRefreshTitle, jobs.TypeScanDownloads, jobs.TypeDownloadMissing:
	default:
		return fmt.Errorf("unknown job type %q", typ)
	}
	if titleID < 0 {
		return fmt.Errorf("invalid title id %d", titleID)
	}

	return nil
}
