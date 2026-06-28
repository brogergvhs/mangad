package downloader

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadImagesReportsSampleErrors(t *testing.T) {
	d := New(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Body: http.NoBody}, nil
	})}, false, t.TempDir(), false)
	d.retryDelay = 0

	_, _, err := d.DownloadImagesConcurrently(
		context.Background(),
		[]string{"https://cdn.test/1.webp", "https://cdn.test/2.webp"},
		filepath.Join(t.TempDir(), "chapter"),
		"https://manga.test/chapter",
		1,
		noProgress{},
	)
	if err == nil {
		t.Fatal("DownloadImagesConcurrently() error = nil")
	}
	if !strings.Contains(err.Error(), "image 1: HTTP 403") {
		t.Fatalf("error = %v", err)
	}
}

type noProgress struct{}

func (noProgress) Update(int, int, int64) {}
func (noProgress) MarkDone()              {}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
