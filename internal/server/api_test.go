package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	chaptersPkg "github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/providers"
	"github.com/brogergvhs/mangad/internal/service"
)

func TestAPISettings(t *testing.T) {
	api, closeDB := testAPI(t)
	defer closeDB()

	var got map[string]string
	requestJSON(t, api, http.MethodGet, "/api/settings", nil, http.StatusOK, &got)
	if got[service.SettingServeRefreshEvery] != "1h" || got[service.SettingServeRunEvery] != "5s" {
		t.Fatalf("default settings = %#v", got)
	}
	if got[service.SettingBrowserSolverEnabled] != "false" || got[service.SettingBrowserSolverEndpoint] == "" {
		t.Fatalf("solver settings = %#v", got)
	}
	if got[service.SettingBrowserDownloaderEnabled] != "false" || got[service.SettingBrowserDownloaderEndpoint] == "" {
		t.Fatalf("browser downloader settings = %#v", got)
	}
	if got[service.SettingJobsWorkers] != "4" {
		t.Fatalf("job worker setting = %#v", got)
	}

	requestJSON(t, api, http.MethodPut, "/api/settings", map[string]string{service.SettingServeDownloadEvery: "15m"}, http.StatusOK, &got)
	if got[service.SettingServeDownloadEvery] != "15m" {
		t.Fatalf("updated settings = %#v", got)
	}

	requestJSON(t, api, http.MethodPut, "/api/settings", map[string]string{service.SettingServeRunEvery: "0s"}, http.StatusBadRequest, nil)
	requestJSON(t, api, http.MethodPut, "/api/settings", map[string]string{service.SettingBrowserSolverTimeoutSeconds: "0"}, http.StatusBadRequest, nil)
	requestJSON(t, api, http.MethodPut, "/api/settings", map[string]string{service.SettingBrowserDownloaderTimeoutSeconds: "0"}, http.StatusBadRequest, nil)
	requestJSON(t, api, http.MethodPut, "/api/settings", map[string]string{service.SettingJobsWorkers: "0"}, http.StatusBadRequest, nil)
}

func TestAPIJobs(t *testing.T) {
	api, closeDB := testAPI(t)
	defer closeDB()

	var job jobs.Job
	requestJSON(t, api, http.MethodPost, "/api/jobs/enqueue", map[string]any{"type": jobs.TypeScanDownloads}, http.StatusCreated, &job)
	if job.Type != jobs.TypeScanDownloads || job.Status != "queued" {
		t.Fatalf("job = %#v", job)
	}

	var all []jobs.Job
	requestJSON(t, api, http.MethodGet, "/api/jobs", nil, http.StatusOK, &all)
	if len(all) != 1 || all[0].ID != job.ID {
		t.Fatalf("jobs = %#v", all)
	}
}

func TestAPIReaderProgress(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "mangad.db")
	db, err := database.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := library.NewRepository(db)
	title, err := repo.AddTitle(ctx, library.AddTitleParams{
		SourceURL:    "https://example.test/manga",
		DisplayTitle: "Example",
		Monitored:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertChapters(ctx, title.ID, []chaptersPkg.Chapter{{
		Chapter: providers.Chapter{URL: "https://example.test/ch-1", Label: "1", NumMain: 1},
	}}); err != nil {
		t.Fatal(err)
	}
	chapter, err := repo.GetChapterByLabel(ctx, title.ID, "1")
	if err != nil {
		t.Fatal(err)
	}
	chapterFile := filepath.Join(t.TempDir(), "chapter-1.cbz")
	if err := os.WriteFile(chapterFile, []byte("not a real cbz"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkDownloadCompleted(ctx, chapter.ID, chapterFile, 100, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	svc, closeDB, err := service.OpenJobs(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	api := New(
		svc,
		func(context.Context) (service.RunSummary, error) { return service.RunSummary{}, nil },
		func(context.Context, string) (service.SourceVerifyResult, error) {
			return service.SourceVerifyResult{}, nil
		},
		"",
	)

	var progress library.TitleReadProgress
	requestJSON(t, api, http.MethodGet, "/api/reader/titles/"+strconv.FormatInt(title.ID, 10), nil, http.StatusOK, &progress)
	if progress.NextChapterID != chapter.ID || progress.NextPage != 1 {
		t.Fatalf("reader progress = %+v, want chapter %d page 1", progress, chapter.ID)
	}

	var status library.ChapterReadStatus
	requestJSON(t, api, http.MethodPost, "/api/reader/chapters/"+strconv.FormatInt(chapter.ID, 10)+"/pages", map[string]int{"page": 1, "total_pages": 2}, http.StatusOK, &status)
	if status.ReadPages != 1 || status.FirstUnreadPage != 2 || status.Completed {
		t.Fatalf("page status = %+v, want page 2 incomplete", status)
	}
	requestJSON(t, api, http.MethodPost, "/api/reader/chapters/"+strconv.FormatInt(chapter.ID, 10)+"/complete", nil, http.StatusOK, &status)
	if !status.Completed || status.FirstUnreadPage != 0 {
		t.Fatalf("complete status = %+v, want complete", status)
	}
}

func TestAPIKey(t *testing.T) {
	api, closeDB := testAPIWithKey(t, "secret")
	defer closeDB()

	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	req.Header.Set("X-API-Key", "secret")
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPISolverHealthDisabled(t *testing.T) {
	api, closeDB := testAPI(t)
	defer closeDB()

	var got map[string]any
	requestJSON(t, api, http.MethodGet, "/api/solver/health", nil, http.StatusOK, &got)
	if got["ok"] != false {
		t.Fatalf("health = %#v", got)
	}
}

func TestAPISourcesList(t *testing.T) {
	api, closeDB := testAPI(t)
	defer closeDB()

	var got []map[string]any
	requestJSON(t, api, http.MethodGet, "/api/sources", nil, http.StatusOK, &got)
	if len(got) == 0 || got[0]["id"] == "" {
		t.Fatalf("sources = %#v", got)
	}
}

func TestAPISourcesLocal(t *testing.T) {
	api, closeDB := testAPI(t)
	defer closeDB()

	profile := map[string]any{
		"id":               "localdemo",
		"name":             "Local Demo",
		"base_url":         "https://local.test/",
		"sample_manga_url": "https://local.test/manga/demo/",
		"enabled":          true,
	}
	requestJSON(t, api, http.MethodPost, "/api/sources/local", profile, http.StatusOK, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sources/export?id=localdemo", nil)
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "id: localdemo") {
		t.Fatalf("export body = %s", rec.Body.String())
	}

	requestJSON(t, api, http.MethodDelete, "/api/sources/local?id=localdemo", nil, http.StatusNoContent, nil)
}

func testAPI(t *testing.T) (http.Handler, func()) {
	return testAPIWithKey(t, "")
}

func testAPIWithKey(t *testing.T, apiKey string) (http.Handler, func()) {
	t.Helper()
	svc, closeDB, err := service.OpenJobs(context.Background(), filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatal(err)
	}
	return New(
		svc,
		func(context.Context) (service.RunSummary, error) { return service.RunSummary{}, nil },
		func(context.Context, string) (service.SourceVerifyResult, error) {
			return service.SourceVerifyResult{}, nil
		},
		apiKey,
	), closeDB
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, body any, wantStatus int, out any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, rec.Code, wantStatus, rec.Body.String())
	}
	if out != nil {
		if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
}
