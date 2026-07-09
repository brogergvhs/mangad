package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	chaptersPkg "github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/providers"
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

func TestApplyBrowserDownloaderSettings(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("OpenJobs() error = %v", err)
	}
	defer closeDB()

	for key, value := range map[string]string{
		SettingBrowserDownloaderEnabled:        "true",
		SettingBrowserDownloaderEndpoint:       "http://browser-worker:8192",
		SettingBrowserDownloaderTimeoutSeconds: "90",
	} {
		if err := svc.SetSetting(ctx, key, value); err != nil {
			t.Fatalf("SetSetting(%s) error = %v", key, err)
		}
	}

	var cfg config.Config
	svc.ApplySettings(ctx, &cfg)
	if !cfg.BrowserDownload.Enabled || cfg.BrowserDownload.Endpoint != "http://browser-worker:8192" || cfg.BrowserDownload.TimeoutSeconds != 90 {
		t.Fatalf("browser downloader config = %#v", cfg.BrowserDownload)
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

func TestEnqueueTitleJobReusesActiveTitleJob(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("OpenJobs() error = %v", err)
	}
	defer closeDB()

	pending, err := svc.enqueue(ctx, jobs.TypeDownloadMissing, JobPayload{TitleID: 123}, time.Now())
	if err != nil {
		t.Fatalf("enqueue(pending) error = %v", err)
	}
	targeted, err := svc.Enqueue(ctx, jobs.TypeDownloadMissing, 123, time.Now())
	if err != nil {
		t.Fatalf("Enqueue(targeted) error = %v", err)
	}
	if targeted.ID != pending.ID {
		t.Fatalf("targeted job ID = %d, want pending ID %d", targeted.ID, pending.ID)
	}
}

func TestGlobalDownloadMissingExpandsToMissingMonitoredTitles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("OpenJobs() error = %v", err)
	}
	defer closeDB()
	want := addJobTitle(t, ctx, svc, "https://example.test/want", true, 1, 0)
	complete := addJobTitle(t, ctx, svc, "https://example.test/complete", true, 1, 1)
	off := addJobTitle(t, ctx, svc, "https://example.test/off", false, 1, 0)

	if err := svc.expandTitleJob(ctx, jobs.TypeDownloadMissing); err != nil {
		t.Fatalf("expandTitleJob() error = %v", err)
	}
	assertTitleJob(t, ctx, svc, jobs.TypeDownloadMissing, want.ID)
	assertNoTitleJob(t, ctx, svc, jobs.TypeDownloadMissing, complete.ID)
	assertNoTitleJob(t, ctx, svc, jobs.TypeDownloadMissing, off.ID)
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
	if hasTitleJob(js, typ, titleID) {
		return
	}
	t.Fatalf("job %s for title %d not found in %#v", typ, titleID, js)
}

func assertNoTitleJob(t *testing.T, ctx context.Context, svc *JobService, typ string, titleID int64) {
	t.Helper()
	js, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if hasTitleJob(js, typ, titleID) {
		t.Fatalf("unexpected job %s for title %d in %#v", typ, titleID, js)
	}
}

func hasTitleJob(js []jobs.Job, typ string, titleID int64) bool {
	for _, job := range js {
		var payload JobPayload
		if job.Type == typ && json.Unmarshal([]byte(job.Payload), &payload) == nil && payload.TitleID == titleID {
			return true
		}
	}
	return false
}

func addJobTitle(t *testing.T, ctx context.Context, svc *JobService, sourceURL string, monitored bool, chapters, completed int) library.Title {
	t.Helper()
	title, err := svc.lib.AddTitle(ctx, library.AddTitleParams{
		SourceURL:    sourceURL,
		DisplayTitle: sourceURL,
		Monitored:    monitored,
	})
	if err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}
	for i := 1; i <= chapters; i++ {
		if _, err := svc.lib.repo.UpsertChapters(ctx, title.ID, []chaptersPkg.Chapter{{
			Chapter: providers.Chapter{
				URL:     sourceURL + "/chapter-" + strconv.Itoa(i),
				Label:   strconv.Itoa(i),
				NumMain: i,
			},
		}}); err != nil {
			t.Fatalf("UpsertChapters() error = %v", err)
		}
	}
	for i := 1; i <= completed; i++ {
		ch, err := svc.lib.repo.GetChapterByLabel(ctx, title.ID, strconv.Itoa(i))
		if err != nil {
			t.Fatalf("GetChapterByLabel() error = %v", err)
		}
		if err := svc.lib.repo.MarkDownloadCompleted(ctx, ch.ID, "/tmp/ch.cbz", 1, 1); err != nil {
			t.Fatalf("MarkDownloadCompleted() error = %v", err)
		}
	}
	title, err = svc.lib.GetTitle(ctx, title.ID)
	if err != nil {
		t.Fatalf("GetTitle() error = %v", err)
	}
	return title
}
