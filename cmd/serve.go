package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/server"
	"github.com/brogergvhs/mangad/internal/service"

	"github.com/spf13/cobra"
)

var (
	flagServeDB            string
	flagServeAddr          string
	flagServeRefreshEvery  time.Duration
	flagServeScanEvery     time.Duration
	flagServeDownloadEvery time.Duration
	flagServeRunEvery      time.Duration
	flagServeAPIKey        string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the automatic MangaD job scheduler",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&flagServeDB, "db", "", "path to MangaD SQLite database")
	serveCmd.Flags().StringVar(&flagServeAddr, "addr", "127.0.0.1:8080", "HTTP API listen address")
	serveCmd.Flags().StringVar(&flagServeAPIKey, "api-key", os.Getenv("MANGAD_API_KEY"), "optional API key required as X-API-Key or Bearer token")
	serveCmd.Flags().DurationVar(&flagServeRefreshEvery, "refresh-every", 0, "refresh schedule, e.g. 1h; 0 disables")
	serveCmd.Flags().DurationVar(&flagServeScanEvery, "scan-every", 0, "download file scan schedule, e.g. 30m; 0 disables")
	serveCmd.Flags().DurationVar(&flagServeDownloadEvery, "download-every", 0, "missing download schedule, e.g. 10m; 0 disables")
	serveCmd.Flags().DurationVar(&flagServeRunEvery, "run-every", 0, "job runner interval, e.g. 5s")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc, closeDB, err := service.OpenJobs(ctx, flagServeDB)
	if err != nil {
		return err
	}
	defer closeDB()
	svc.SetRuntimeConfig(runtimeConfig)

	if err := seedServeSetting(cmd, svc, ctx, "refresh-every", service.SettingServeRefreshEvery, flagServeRefreshEvery); err != nil {
		return err
	}
	if err := seedServeSetting(cmd, svc, ctx, "scan-every", service.SettingServeScanEvery, flagServeScanEvery); err != nil {
		return err
	}
	if err := seedServeSetting(cmd, svc, ctx, "download-every", service.SettingServeDownloadEvery, flagServeDownloadEvery); err != nil {
		return err
	}
	if err := seedServeSetting(cmd, svc, ctx, "run-every", service.SettingServeRunEvery, flagServeRunEvery); err != nil {
		return err
	}

	httpSrv := &http.Server{Addr: flagServeAddr, Handler: server.New(
		svc,
		func(ctx context.Context) (service.RunSummary, error) {
			return runDue(ctx, svc)
		},
		func(ctx context.Context, sourceID string) (service.SourceVerifyResult, error) {
			cfg, logSvc, err := svc.RuntimeConfig(ctx)
			if err != nil {
				return service.SourceVerifyResult{}, err
			}
			return svc.VerifySource(ctx, cfg, logSvc, sourceID)
		},
		flagServeAPIKey,
	)}
	httpErr := make(chan error, 1)
	go func() {
		fmt.Printf("HTTP API listening on %s\n", flagServeAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErr <- err
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	nextRefresh := time.Now()
	nextScan := time.Now()
	nextDownload := time.Now()
	var refreshEvery, scanEvery, downloadEvery, runEvery time.Duration

	for {
		oldRefresh, oldScan, oldDownload, oldRun := refreshEvery, scanEvery, downloadEvery, runEvery
		refreshEvery = serveDuration(svc, ctx, service.SettingServeRefreshEvery)
		scanEvery = serveDuration(svc, ctx, service.SettingServeScanEvery)
		downloadEvery = serveDuration(svc, ctx, service.SettingServeDownloadEvery)
		runEvery = serveDuration(svc, ctx, service.SettingServeRunEvery)
		if runEvery <= 0 {
			runEvery = fallbackDuration(service.SettingDefault(service.SettingServeRunEvery))
		}
		changed := oldRefresh != refreshEvery || oldScan != scanEvery || oldDownload != downloadEvery || oldRun != runEvery
		if oldRefresh != refreshEvery {
			nextRefresh = time.Now()
		}
		if oldScan != scanEvery {
			nextScan = time.Now()
		}
		if oldDownload != downloadEvery {
			nextDownload = time.Now()
		}
		if changed {
			fmt.Printf("Serving: refresh=%s scan=%s download=%s run=%s\n", refreshEvery, scanEvery, downloadEvery, runEvery)
		}

		// Transient failures (e.g. SQLITE_BUSY) must not kill the daemon;
		// shutdown is handled by the ctx.Done select below.
		if err := serveTick(ctx, svc, nextDue(&nextRefresh, refreshEvery), nextDue(&nextScan, scanEvery), nextDue(&nextDownload, downloadEvery)); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "serve tick: %v\n", err)
		}
		if _, err := runDue(ctx, svc); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "run due jobs: %v\n", err)
		}

		timer := time.NewTimer(runEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case err := <-httpErr:
			timer.Stop()
			return err
		case <-timer.C:
		}
	}
}

func serveTick(ctx context.Context, svc *service.JobService, refresh, scan, download bool) error {
	now := time.Now()
	if refresh {
		if _, err := svc.Enqueue(ctx, jobs.TypeRefreshTitle, 0, now); err != nil {
			return err
		}
	}
	if scan {
		if _, err := svc.Enqueue(ctx, jobs.TypeScanDownloads, 0, now); err != nil {
			return err
		}
	}
	if download {
		if _, err := svc.Enqueue(ctx, jobs.TypeDownloadMissing, 0, now); err != nil {
			return err
		}
	}
	return nil
}

func runDue(ctx context.Context, svc *service.JobService) (service.RunSummary, error) {
	cfg, logSvc, err := svc.RuntimeConfig(ctx)
	if err != nil {
		return service.RunSummary{}, err
	}
	summary, err := svc.RunDue(ctx, cfg, logSvc)
	if err != nil {
		return summary, err
	}
	if summary.Done != 0 || summary.Failed != 0 {
		fmt.Printf("Jobs run: %d done, %d failed\n", summary.Done, summary.Failed)
	}
	return summary, nil
}

func seedServeSetting(cmd *cobra.Command, svc *service.JobService, ctx context.Context, flagName, key string, value time.Duration) error {
	if !cmd.Flags().Changed(flagName) {
		return nil
	}
	return svc.SetSetting(ctx, key, value.String())
}

func serveDuration(svc *service.JobService, ctx context.Context, key string) time.Duration {
	fallback := service.SettingDefault(key)
	value := svc.Setting(ctx, key, fallback)
	d, err := time.ParseDuration(value)
	if err != nil {
		return fallbackDuration(fallback)
	}
	return d
}

func nextDue(next *time.Time, every time.Duration) bool {
	if every <= 0 || time.Now().Before(*next) {
		return false
	}
	*next = time.Now().Add(every)
	return true
}

func fallbackDuration(value string) time.Duration {
	d, _ := time.ParseDuration(value)
	return d
}
