package server

import (
	"encoding/json"
	"net/http"
	"runtime/debug"
	"sort"

	"github.com/brogergvhs/kaodoku/internal/auth"
	"github.com/brogergvhs/kaodoku/internal/service"
)

const maxPageBytes = 64 << 20 // per-image download cap

type apiV1 struct{ svc *service.JobService }

// registerAPIV1 mounts the /api/v1 surface consumed by the native app.
func registerAPIV1(mux *http.ServeMux, svc *service.JobService) {
	a := &apiV1{svc: svc}
	mux.HandleFunc("GET /api/v1/meta", a.meta)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("GET /api/v1/me", a.me)
	mux.HandleFunc("DELETE /api/v1/auth/token", a.revokeToken)
}

// v1err writes the {error, code} envelope shared by every v1 endpoint.
func v1err(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

func serverVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

type metaDTO struct {
	ServerVersion string   `json:"server_version"`
	APIVersion    int      `json:"api_version"`
	AuthRequired  bool     `json:"auth_required"`
	Features      []string `json:"features"`
	ImageFormats  []string `json:"image_formats"`
	MaxPageBytes  int64    `json:"max_page_bytes"`
}

type meDTO struct {
	User        meUserDTO  `json:"user"`
	Permissions []string   `json:"permissions"`
	AniList     aniListDTO `json:"anilist"`
}

type meUserDTO struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Role       string `json:"role"`
	AllowAdult bool   `json:"allow_adult"`
}

type aniListDTO struct {
	Connected bool   `json:"connected"`
	Name      string `json:"name"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// meDTOFor builds the current-user payload (identity, permissions, AniList link).
func (a *apiV1) meDTOFor(r *http.Request, user *auth.User) meDTO {
	perms := make([]string, 0, len(user.Perms))
	for p := range user.Perms {
		perms = append(perms, p)
	}
	sort.Strings(perms)
	conn := a.svc.AniListConnectionFor(r.Context(), user.ID)
	return meDTO{
		User:        meUserDTO{ID: user.ID, Username: user.Username, Role: user.RoleName, AllowAdult: user.AllowAdult},
		Permissions: perms,
		AniList:     aniListDTO{Connected: conn.Connected, Name: conn.Name, ExpiresAt: conn.ExpiresAt},
	}
}

func (a *apiV1) meta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, metaDTO{
		ServerVersion: serverVersion(),
		APIVersion:    1,
		AuthRequired:  authEnabled(),
		Features:      []string{},
		ImageFormats:  []string{"jpg", "jpeg", "png", "webp", "gif", "avif"},
		MaxPageBytes:  maxPageBytes,
	})
}

// login verifies credentials and mints a never-expiring device API token.
func (a *apiV1) login(w http.ResponseWriter, r *http.Request) {
	if !authEnabled() {
		v1err(w, http.StatusBadRequest, "bad_request", "server is in single-user mode; login is not required")
		return
	}
	var body struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		DeviceName string `json:"device_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	id, err := a.svc.Auth().Authenticate(r.Context(), body.Username, body.Password)
	if err != nil {
		v1err(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	name := body.DeviceName
	if name == "" {
		name = "iOS app"
	}
	token, err := a.svc.Auth().CreateAPIToken(r.Context(), id, name, 0)
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	user, err := a.svc.Auth().GetUser(r.Context(), id)
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "me": a.meDTOFor(r, user)})
}

func (a *apiV1) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.meDTOFor(r, userFrom(r.Context())))
}

// revokeToken deletes the calling device's API token (sign out).
func (a *apiV1) revokeToken(w http.ResponseWriter, r *http.Request) {
	if token := headerToken(r); token != "" {
		if err := a.svc.Auth().RevokeAPIToken(r.Context(), token); err != nil {
			v1err(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
