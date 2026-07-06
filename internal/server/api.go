// Package server exposes the MangaD HTTP API.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/service"
	"github.com/brogergvhs/mangad/internal/sources"
)

// New returns the HTTP API handler.
func New(
	svc *service.JobService,
	runJobs func(context.Context) (service.RunSummary, error),
	verifySource func(context.Context, string) (service.SourceVerifyResult, error),
	apiKeys ...string,
) http.Handler {
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
				if err := service.ValidateSetting(key, value); err != nil {
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

	mux.HandleFunc("/api/wanted", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			items, err := svc.ListWanted(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, items)
		case http.MethodPost:
			var req struct {
				AniListID int `json:"anilist_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
			item, err := svc.AddAniListWanted(r.Context(), req.AniListID)
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, http.StatusCreated, item)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/wanted/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		items, err := svc.SearchAniList(r.Context(), r.URL.Query().Get("q"), 10)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, items)
	})

	mux.HandleFunc("/api/wanted/matches", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			id, err := parseInt64Query(r, "catalog_id")
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			matches, err := svc.ListMatches(r.Context(), id)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, matches)
		case http.MethodPost:
			var req struct {
				CatalogID int64 `json:"catalog_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
			matches, err := svc.MatchSources(r.Context(), req.CatalogID)
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, matches)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/wanted/track", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			MatchID         int64  `json:"match_id"`
			Output          string `json:"output"`
			Monitored       *bool  `json:"monitored"`
			RefreshInterval string `json:"refresh_interval"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		monitored := true
		if req.Monitored != nil {
			monitored = *req.Monitored
		}
		title, err := svc.TrackMatch(r.Context(), req.MatchID, req.Output, monitored, req.RefreshInterval)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, title)
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
			Type      string `json:"type"`
			TitleID   int64  `json:"title_id"`
			SourceID  string `json:"source_id"`
			CatalogID int64  `json:"catalog_id"`
			Delay     string `json:"delay"`
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
		var job any
		var err error
		if req.Type == jobs.TypeVerifySource {
			job, err = svc.EnqueueSource(r.Context(), req.SourceID, runAfter)
		} else if req.Type == jobs.TypeMatchSources {
			job, err = svc.EnqueueCatalog(r.Context(), req.Type, req.CatalogID, runAfter)
		} else {
			job, err = svc.Enqueue(r.Context(), req.Type, req.TitleID, runAfter)
		}
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

	mux.HandleFunc("/api/sources", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := svc.SyncSources(r.Context(), ""); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		sources, err := svc.ListSources(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sources)
	})

	mux.HandleFunc("/api/sources/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			RegistryURL string `json:"registry_url"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		if req.RegistryURL == "" {
			req.RegistryURL = svc.Setting(r.Context(), service.SettingSourceRegistryURL, "")
		}
		if req.RegistryURL != "" {
			if err := service.ValidateSetting(service.SettingSourceRegistryURL, req.RegistryURL); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if err := svc.SetSetting(r.Context(), service.SettingSourceRegistryURL, req.RegistryURL); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if err := svc.SyncSources(r.Context(), req.RegistryURL); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		sources, err := svc.ListSources(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, sources)
	})

	mux.HandleFunc("/api/sources/local", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var profile sources.Profile
			if err := json.NewDecoder(r.Body).Decode(&profile); err != nil {
				writeError(w, http.StatusBadRequest, "invalid json")
				return
			}
			if err := svc.ImportLocalSource(r.Context(), profile); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, profile)
		case http.MethodDelete:
			id := r.URL.Query().Get("id")
			if err := svc.RemoveLocalSource(r.Context(), id); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/sources/export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := svc.SyncSources(r.Context(), ""); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		body, err := svc.ExportSource(r.Context(), r.URL.Query().Get("id"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})

	mux.HandleFunc("/api/sources/verify", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := svc.SyncSources(r.Context(), ""); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		result, err := verifySource(r.Context(), req.ID)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, result)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	mux.HandleFunc("/api/solver/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ok, err := svc.BrowserSolverHealth(r.Context())
		status := http.StatusOK
		if err != nil {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, map[string]any{"ok": ok, "error": errorString(err)})
	})
	if len(apiKeys) == 0 || strings.TrimSpace(apiKeys[0]) == "" {
		return mux
	}
	return requireAPIKey(mux, strings.TrimSpace(apiKeys[0]))
}

func requireAPIKey(next http.Handler, key string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-API-Key")
		if token == "" {
			token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(key)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func serveSettings(r *http.Request, svc *service.JobService) map[string]string {
	out := map[string]string{}
	for _, key := range service.SettingKeys() {
		out[key] = svc.Setting(r.Context(), key, service.SettingDefault(key))
	}
	return out
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func parseInt64Query(r *http.Request, key string) (int64, error) {
	value := r.URL.Query().Get(key)
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, strconv.ErrSyntax
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
