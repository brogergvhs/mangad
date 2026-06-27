// Package server exposes the MangaD HTTP API.
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/brogergvhs/mangad/internal/service"
)

// New returns the HTTP API handler.
func New(svc *service.JobService, runJobs func(context.Context) (service.RunSummary, error)) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, serveSettings(r, svc))
		case http.MethodPut:
			var values map[string]string
			if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
			for key, value := range values {
				if err := service.ValidateServeSetting(key, value); err != nil {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				if err := svc.SetSetting(r.Context(), key, value); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			writeJSON(w, http.StatusOK, serveSettings(r, svc))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/library", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		titles, err := svc.ListTitles(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, titles)
	})
	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		jobs, err := svc.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, jobs)
	})
	mux.HandleFunc("/api/jobs/enqueue", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Type    string `json:"type"`
			TitleID int64  `json:"title_id"`
			Delay   string `json:"delay"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		runAfter := time.Now()
		if req.Delay != "" {
			delay, err := time.ParseDuration(req.Delay)
			if err != nil || delay < 0 {
				writeError(w, http.StatusBadRequest, "invalid delay")
				return
			}
			runAfter = runAfter.Add(delay)
		}
		job, err := svc.Enqueue(r.Context(), req.Type, req.TitleID, runAfter)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, job)
	})
	mux.HandleFunc("/api/jobs/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		summary, err := runJobs(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, summary)
	})
	return mux
}

func serveSettings(r *http.Request, svc *service.JobService) map[string]string {
	out := map[string]string{}
	for _, key := range []string{
		service.SettingServeRefreshEvery,
		service.SettingServeScanEvery,
		service.SettingServeDownloadEvery,
		service.SettingServeRunEvery,
	} {
		out[key] = svc.Setting(r.Context(), key, service.ServeSettingDefault(key))
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
