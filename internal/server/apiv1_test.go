package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func do(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-API-Key", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPIV1AuthFlow(t *testing.T) {
	t.Setenv("KAODOKU_ADMIN_USER", "boss")
	t.Setenv("KAODOKU_ADMIN_PASSWORD", "secret123")
	api, closeDB := testAPI(t)
	defer closeDB()

	// meta is public and reports auth is on.
	var meta metaDTO
	rec := do(t, api, http.MethodGet, "/api/v1/meta", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("meta status = %d", rec.Code)
	}
	_ = json.NewDecoder(rec.Body).Decode(&meta)
	if !meta.AuthRequired || meta.APIVersion != 1 {
		t.Fatalf("meta = %+v", meta)
	}

	// wrong password -> 401.
	if rec := do(t, api, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "boss", "password": "nope"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login status = %d", rec.Code)
	}

	// login mints a token and returns me.
	rec = do(t, api, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "boss", "password": "secret123", "device_name": "test"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var login struct {
		Token string `json:"token"`
		Me    meDTO  `json:"me"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&login)
	if login.Token == "" || login.Me.User.Username != "boss" || len(login.Me.Permissions) == 0 {
		t.Fatalf("login body = %+v", login)
	}

	// me requires the token.
	if rec := do(t, api, http.MethodGet, "/api/v1/me", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me without token = %d", rec.Code)
	}
	if rec := do(t, api, http.MethodGet, "/api/v1/me", login.Token, nil); rec.Code != http.StatusOK {
		t.Fatalf("me with token = %d; body=%s", rec.Code, rec.Body.String())
	}

	// revoke, then the token no longer authenticates.
	if rec := do(t, api, http.MethodDelete, "/api/v1/auth/token", login.Token, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d", rec.Code)
	}
	if rec := do(t, api, http.MethodGet, "/api/v1/me", login.Token, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after revoke = %d, want 401", rec.Code)
	}
}

func TestAPIV1MetaSingleUser(t *testing.T) {
	api, closeDB := testAPI(t) // no KAODOKU_ADMIN_PASSWORD -> single-user
	defer closeDB()
	var meta metaDTO
	rec := do(t, api, http.MethodGet, "/api/v1/meta", "", nil)
	_ = json.NewDecoder(rec.Body).Decode(&meta)
	if meta.AuthRequired {
		t.Fatalf("single-user meta.auth_required = true")
	}
	if rec := do(t, api, http.MethodPost, "/api/v1/auth/login", "", map[string]string{"username": "x", "password": "y"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("single-user login status = %d, want 400", rec.Code)
	}
}
