package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A 429 with Retry-After must be waited out and retried, not surfaced.
func TestAniListDoRetriesAfter429(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits <= 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	c := NewAniListClient(srv.Client())
	c.endpoint = srv.URL
	var out struct{}
	if err := c.do(context.Background(), `query { x }`, nil, &out); err != nil {
		t.Fatalf("do after 429s: %v", err)
	}
	if hits != 3 {
		t.Fatalf("expected 3 attempts (2 rate-limited), got %d", hits)
	}
}

// Persistent 429s give up after the attempt budget with a clear error.
func TestAniListDoGivesUpOnPersistent429(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewAniListClient(srv.Client())
	c.endpoint = srv.URL
	var out struct{}
	err := c.do(context.Background(), `query { x }`, nil, &out)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected rate-limited error, got %v", err)
	}
	if hits != 3 {
		t.Fatalf("expected 3 attempts, got %d", hits)
	}
}
