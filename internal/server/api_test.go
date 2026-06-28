package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/brogergvhs/mangad/internal/jobs"
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

	requestJSON(t, api, http.MethodPut, "/api/settings", map[string]string{service.SettingServeDownloadEvery: "15m"}, http.StatusOK, &got)
	if got[service.SettingServeDownloadEvery] != "15m" {
		t.Fatalf("updated settings = %#v", got)
	}

	requestJSON(t, api, http.MethodPut, "/api/settings", map[string]string{service.SettingServeRunEvery: "0s"}, http.StatusBadRequest, nil)
	requestJSON(t, api, http.MethodPut, "/api/settings", map[string]string{service.SettingBrowserSolverTimeoutSeconds: "0"}, http.StatusBadRequest, nil)
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

func TestAPISolverHealthDisabled(t *testing.T) {
	api, closeDB := testAPI(t)
	defer closeDB()

	var got map[string]any
	requestJSON(t, api, http.MethodGet, "/api/solver/health", nil, http.StatusOK, &got)
	if got["ok"] != false {
		t.Fatalf("health = %#v", got)
	}
}

func testAPI(t *testing.T) (http.Handler, func()) {
	t.Helper()
	svc, closeDB, err := service.OpenJobs(context.Background(), filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatal(err)
	}
	return New(svc, func(context.Context) (service.RunSummary, error) { return service.RunSummary{}, nil }), closeDB
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
