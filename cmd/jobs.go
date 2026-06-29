package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/service"

	"github.com/spf13/cobra"
)

var (
	flagJobsDB        string
	flagJobsTitleID   int64
	flagJobsSourceID  string
	flagJobsCatalogID int64
	flagJobsDelay     time.Duration
)

var jobsCmd = &cobra.Command{Use: "jobs", Short: "Manage background jobs"}

var jobsListCmd = &cobra.Command{Use: "list", Short: "List recent jobs", RunE: runJobsList}

var jobsRunCmd = &cobra.Command{Use: "run", Short: "Run due jobs until the queue is empty", RunE: runJobsRun}

var jobsEnqueueCmd = &cobra.Command{
	Use:   "enqueue <refresh_title|scan_downloads|download_missing|verify_source|match_sources>",
	Short: "Enqueue a background job",
	Args:  cobra.ExactArgs(1),
	RunE:  runJobsEnqueue,
}

func init() {
	jobsCmd.PersistentFlags().StringVar(&flagJobsDB, "db", "", "path to MangaD SQLite database")
	jobsEnqueueCmd.Flags().Int64Var(&flagJobsTitleID, "title", 0, "title id; omit or use 0 for all monitored titles")
	jobsEnqueueCmd.Flags().StringVar(&flagJobsSourceID, "source", "", "source id for verify_source jobs")
	jobsEnqueueCmd.Flags().Int64Var(&flagJobsCatalogID, "catalog", 0, "catalog manga id for match_sources jobs")
	jobsEnqueueCmd.Flags().DurationVar(&flagJobsDelay, "delay", 0, "delay before the job becomes due")

	jobsCmd.AddCommand(jobsListCmd, jobsRunCmd, jobsEnqueueCmd)
	rootCmd.AddCommand(jobsCmd)
}

func runJobsEnqueue(_ *cobra.Command, args []string) error {
	return withJobs(func(ctx context.Context, svc *service.JobService) error {
		var (
			job jobs.Job
			err error
		)
		switch args[0] {
		case jobs.TypeVerifySource:
			job, err = svc.EnqueueSource(ctx, flagJobsSourceID, time.Now().Add(flagJobsDelay))
		case jobs.TypeMatchSources:
			job, err = svc.EnqueueCatalog(ctx, args[0], flagJobsCatalogID, time.Now().Add(flagJobsDelay))
		default:
			job, err = svc.Enqueue(ctx, args[0], flagJobsTitleID, time.Now().Add(flagJobsDelay))
		}
		if err != nil {
			return err
		}
		printJob(job)
		return nil
	})
}

func runJobsList(_ *cobra.Command, _ []string) error {
	return withJobs(func(ctx context.Context, svc *service.JobService) error {
		all, err := svc.List(ctx)
		if err != nil {
			return err
		}
		if len(all) == 0 {
			fmt.Println("No jobs.")
			return nil
		}
		for _, job := range all {
			printJob(job)
		}
		return nil
	})
}

func runJobsRun(_ *cobra.Command, _ []string) error {
	return withJobs(func(ctx context.Context, svc *service.JobService) error {
		cfg, logSvc, err := runtimeConfig()
		if err != nil {
			return err
		}
		cfg.CookieDBPath = flagJobsDB
		summary, err := svc.RunDue(ctx, cfg, logSvc)
		if err != nil {
			return err
		}
		fmt.Printf("Jobs run: %d done, %d failed\n", summary.Done, summary.Failed)
		return nil
	})
}

func withJobs(fn func(context.Context, *service.JobService) error) error {
	ctx := context.Background()
	svc, closeDB, err := service.OpenJobs(ctx, flagJobsDB)
	if err != nil {
		return err
	}
	defer closeDB()
	return fn(ctx, svc)
}

func printJob(job jobs.Job) {
	fmt.Printf("%3d  %-16s %-8s attempts=%d run_after=%s", job.ID, job.Type, job.Status, job.Attempts, job.RunAfter.Local().Format(time.RFC3339))
	if titleID := payloadTitleID(job.Payload); titleID > 0 {
		fmt.Printf(" title=%d", titleID)
	}
	if sourceID := payloadSourceID(job.Payload); sourceID != "" {
		fmt.Printf(" source=%s", sourceID)
	}
	if catalogID := payloadCatalogID(job.Payload); catalogID > 0 {
		fmt.Printf(" catalog=%d", catalogID)
	}
	if job.LastError != "" {
		fmt.Printf(" error=%q", job.LastError)
	}
	fmt.Println()
}

func payloadCatalogID(payload string) int64 {
	var p service.JobPayload
	_ = json.Unmarshal([]byte(payload), &p)
	return p.CatalogID
}

func payloadSourceID(payload string) string {
	var p service.JobPayload
	_ = json.Unmarshal([]byte(payload), &p)
	return p.SourceID
}

func payloadTitleID(payload string) int64 {
	var p service.JobPayload
	_ = json.Unmarshal([]byte(payload), &p)
	return p.TitleID
}
