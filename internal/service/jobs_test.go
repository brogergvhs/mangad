package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/brogergvhs/mangad/internal/jobs"
)

func TestApplyLimitsFromSettings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("OpenJobs() error = %v", err)
	}
	defer closeDB()

	// Defaults before any override.
	if svc.jobs.MaxAttempts != 3 || svc.lib.repo.MaxDownloadAttempts != 3 || svc.jobTimeout != defaultJobTimeout {
		t.Fatalf("defaults = %d/%d/%s", svc.jobs.MaxAttempts, svc.lib.repo.MaxDownloadAttempts, svc.jobTimeout)
	}

	for key, value := range map[string]string{
		SettingJobsMaxAttempts:      "5",
		SettingDownloadsMaxAttempts: "7",
		SettingJobsTimeout:          "2m",
	} {
		if err := svc.SetSetting(ctx, key, value); err != nil {
			t.Fatalf("SetSetting(%s) error = %v", key, err)
		}
	}
	svc.applyLimits(ctx)

	if svc.jobs.MaxAttempts != 5 {
		t.Errorf("jobs.MaxAttempts = %d, want 5", svc.jobs.MaxAttempts)
	}
	if svc.lib.repo.MaxDownloadAttempts != 7 {
		t.Errorf("downloads MaxDownloadAttempts = %d, want 7", svc.lib.repo.MaxDownloadAttempts)
	}
	if svc.jobTimeout != 2*time.Minute {
		t.Errorf("jobTimeout = %s, want 2m", svc.jobTimeout)
	}
}

func TestEnqueueTitleJobReusesActiveGlobalJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("OpenJobs() error = %v", err)
	}
	defer closeDB()

	global, err := svc.Enqueue(ctx, jobs.TypeDownloadMissing, 0, time.Now())
	if err != nil {
		t.Fatalf("Enqueue(global) error = %v", err)
	}
	targeted, err := svc.Enqueue(ctx, jobs.TypeDownloadMissing, 123, time.Now())
	if err != nil {
		t.Fatalf("Enqueue(targeted) error = %v", err)
	}
	if targeted.ID != global.ID {
		t.Fatalf("targeted job ID = %d, want global ID %d", targeted.ID, global.ID)
	}
}
