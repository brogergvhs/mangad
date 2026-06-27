package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/service"

	"github.com/spf13/cobra"
)

var (
	flagServeDB            string
	flagServeRefreshEvery  time.Duration
	flagServeScanEvery     time.Duration
	flagServeDownloadEvery time.Duration
	flagServeRunEvery      time.Duration
)

const (
	settingServeRefreshEvery  = "serve.refresh_every"
	settingServeScanEvery     = "serve.scan_every"
	settingServeDownloadEvery = "serve.download_every"
	settingServeRunEvery      = "serve.run_every"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the automatic MangaD job scheduler",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&flagServeDB, "db", "", "path to MangaD SQLite database")
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

	if err := seedServeSetting(cmd, svc, ctx, "refresh-every", settingServeRefreshEvery, flagServeRefreshEvery); err != nil {
		return err
	}
	if err := seedServeSetting(cmd, svc, ctx, "scan-every", settingServeScanEvery, flagServeScanEvery); err != nil {
		return err
	}
	if err := seedServeSetting(cmd, svc, ctx, "download-every", settingServeDownloadEvery, flagServeDownloadEvery); err != nil {
		return err
	}
	if err := seedServeSetting(cmd, svc, ctx, "run-every", settingServeRunEvery, flagServeRunEvery); err != nil {
		return err
	}

	nextRefresh := time.Now()
	nextScan := time.Now()
	nextDownload := time.Now()
	var refreshEvery, scanEvery, downloadEvery, runEvery time.Duration

	for {
		oldRefresh, oldScan, oldDownload, oldRun := refreshEvery, scanEvery, downloadEvery, runEvery
		refreshEvery = serveDuration(svc, ctx, settingServeRefreshEvery, "1h")
		scanEvery = serveDuration(svc, ctx, settingServeScanEvery, "30m")
		downloadEvery = serveDuration(svc, ctx, settingServeDownloadEvery, "10m")
		runEvery = serveDuration(svc, ctx, settingServeRunEvery, "5s")
		if runEvery <= 0 {
			runEvery = fallbackDuration("5s")
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

		if err := serveTick(ctx, svc, nextDue(&nextRefresh, refreshEvery), nextDue(&nextScan, scanEvery), nextDue(&nextDownload, downloadEvery)); err != nil {
			return err
		}
		if err := runDue(ctx, svc); err != nil {
			return err
		}

		timer := time.NewTimer(runEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
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

func runDue(ctx context.Context, svc *service.JobService) error {
	cfg, logSvc, err := runtimeConfig()
	if err != nil {
		return err
	}
	summary, err := svc.RunDue(ctx, cfg, logSvc)
	if err != nil {
		return err
	}
	if summary.Done != 0 || summary.Failed != 0 {
		fmt.Printf("Jobs run: %d done, %d failed\n", summary.Done, summary.Failed)
	}
	return nil
}

func seedServeSetting(cmd *cobra.Command, svc *service.JobService, ctx context.Context, flagName, key string, value time.Duration) error {
	if !cmd.Flags().Changed(flagName) {
		return nil
	}
	return svc.SetSetting(ctx, key, value.String())
}

func serveDuration(svc *service.JobService, ctx context.Context, key, fallback string) time.Duration {
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
