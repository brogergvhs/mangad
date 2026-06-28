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
	flagJobsDB      string
	flagJobsTitleID int64
	flagJobsDelay   time.Duration
)

var jobsCmd = &cobra.Command{Use: "jobs", Short: "Manage background jobs"}

var jobsListCmd = &cobra.Command{Use: "list", Short: "List recent jobs", RunE: runJobsList}

var jobsRunCmd = &cobra.Command{Use: "run", Short: "Run due jobs until the queue is empty", RunE: runJobsRun}

var jobsEnqueueCmd = &cobra.Command{
	Use:   "enqueue <refresh_title|scan_downloads|download_missing>",
	Short: "Enqueue a background job",
	Args:  cobra.ExactArgs(1),
	RunE:  runJobsEnqueue,
}

func init() {
	jobsCmd.PersistentFlags().StringVar(&flagJobsDB, "db", "", "path to MangaD SQLite database")
	jobsEnqueueCmd.Flags().Int64Var(&flagJobsTitleID, "title", 0, "title id; omit or use 0 for all monitored titles")
	jobsEnqueueCmd.Flags().DurationVar(&flagJobsDelay, "delay", 0, "delay before the job becomes due")

	jobsCmd.AddCommand(jobsListCmd, jobsRunCmd, jobsEnqueueCmd)
	rootCmd.AddCommand(jobsCmd)
}

func runJobsEnqueue(_ *cobra.Command, args []string) error {
	return withJobs(func(ctx context.Context, svc *service.JobService) error {
		job, err := svc.Enqueue(ctx, args[0], flagJobsTitleID, time.Now().Add(flagJobsDelay))
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
	if job.LastError != "" {
		fmt.Printf(" error=%q", job.LastError)
	}
	fmt.Println()
}

func payloadTitleID(payload string) int64 {
	var p service.JobPayload
	_ = json.Unmarshal([]byte(payload), &p)
	return p.TitleID
}
