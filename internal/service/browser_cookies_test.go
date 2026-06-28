package service

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestBrowserCookieCacheSaveLoad(t *testing.T) {
	cache := browserCookieCache{dbPath: filepath.Join(t.TempDir(), "mangad.db")}
	err := cache.save(t.Context(), "https://manga.test/chapter", "ua", []*http.Cookie{{
		Name:    "cf_clearance",
		Value:   "token",
		Domain:  ".manga.test",
		Path:    "/",
		Expires: time.Now().Add(time.Hour),
		Secure:  true,
	}})
	if err != nil {
		t.Fatal(err)
	}

	session, err := cache.load(t.Context(), "https://img.manga.test/page.webp")
	if err != nil {
		t.Fatal(err)
	}
	if session.userAgent != "ua" {
		t.Fatalf("userAgent = %q", session.userAgent)
	}
	if len(session.cookies) != 1 || session.cookies[0].Name != "cf_clearance" || session.cookies[0].Value != "token" {
		t.Fatalf("cookies = %#v", session.cookies)
	}
}

func TestBrowserCookieCacheSkipsExpired(t *testing.T) {
	cache := browserCookieCache{dbPath: filepath.Join(t.TempDir(), "mangad.db")}
	err := cache.save(t.Context(), "https://manga.test/chapter", "ua", []*http.Cookie{{
		Name:    "expired",
		Value:   "token",
		Domain:  ".manga.test",
		Path:    "/",
		Expires: time.Now().Add(-time.Hour),
	}})
	if err != nil {
		t.Fatal(err)
	}

	session, err := cache.load(t.Context(), "https://manga.test/chapter")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.cookies) != 0 {
		t.Fatalf("cookies = %#v", session.cookies)
	}
}
