package service

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/sources"
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

func TestLinkTitleSourceURLQueuesRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("OpenJobs() error = %v", err)
	}
	defer closeDB()
	addTestSource(t, ctx, svc, "demo")
	title, err := svc.lib.AddTitle(ctx, library.AddTitleParams{
		SourceURL:    "pending:demo",
		DisplayTitle: "Demo",
		Monitored:    true,
	})
	if err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}

	if _, err := svc.LinkTitleSourceURL(ctx, title.ID, "demo", "https://demo.test/manga/demo"); err != nil {
		t.Fatalf("LinkTitleSourceURL() error = %v", err)
	}
	assertTitleJob(t, ctx, svc, jobs.TypeRefreshTitle, title.ID)
}

func TestSourceChangeQueuesRefreshForActiveTitles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("OpenJobs() error = %v", err)
	}
	defer closeDB()
	addTestSource(t, ctx, svc, "demo")
	title, err := svc.lib.AddTitle(ctx, library.AddTitleParams{
		SourceID:     "demo",
		SourceURL:    "https://demo.test/manga/demo",
		DisplayTitle: "Demo",
		Monitored:    true,
	})
	if err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}

	if err := svc.SetSourceMethods(ctx, "demo", sources.FetchHTTP, sources.FetchBrowser); err != nil {
		t.Fatalf("SetSourceMethods() error = %v", err)
	}
	assertTitleJob(t, ctx, svc, jobs.TypeRefreshTitle, title.ID)
}

func addTestSource(t *testing.T, ctx context.Context, svc *JobService, id string) {
	t.Helper()
	if err := svc.ImportLocalSource(ctx, sources.Profile{
		ID:                id,
		Name:              id,
		Domains:           []string{id + ".test"},
		BaseURL:           "https://" + id + ".test/",
		SampleMangaURL:    "https://" + id + ".test/manga/demo",
		Scraper:           "generic",
		AllowedExtensions: []string{"jpg"},
		Enabled:           true,
	}); err != nil {
		t.Fatalf("ImportLocalSource() error = %v", err)
	}
}

func assertTitleJob(t *testing.T, ctx context.Context, svc *JobService, typ string, titleID int64) {
	t.Helper()
	js, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, job := range js {
		if job.Type == typ && job.Payload == `{"title_id":`+strconv.FormatInt(titleID, 10)+`}` {
			return
		}
	}
	t.Fatalf("job %s for title %d not found in %#v", typ, titleID, js)
}
