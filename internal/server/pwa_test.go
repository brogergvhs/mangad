package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPWARoutes(t *testing.T) {
	api, closeDB := testAPI(t)
	defer closeDB()

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	sw := get("/sw.js")
	if sw.Code != http.StatusOK {
		t.Fatalf("/sw.js status = %d", sw.Code)
	}
	if ct := sw.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("/sw.js content-type = %q", ct)
	}
	if sw.Header().Get("Service-Worker-Allowed") != "/" {
		t.Error("/sw.js missing Service-Worker-Allowed: /")
	}
	if body := sw.Body.String(); strings.Contains(body, "__VERSION__") || !strings.Contains(body, "shell-") {
		t.Error("/sw.js version placeholder not substituted")
	}

	mani := get("/static/manifest.webmanifest")
	if mani.Code != http.StatusOK || !strings.Contains(mani.Body.String(), "\"standalone\"") {
		t.Fatalf("manifest status = %d body = %s", mani.Code, mani.Body.String())
	}
	if icon := get("/static/icon-192.png"); icon.Code != http.StatusOK {
		t.Errorf("icon status = %d", icon.Code)
	}
}
