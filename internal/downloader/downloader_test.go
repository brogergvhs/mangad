package downloader

import (
	"context"
	"io"
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

func TestDownloadImagesAcceptsBinaryImageByURLExtension(t *testing.T) {
	d := New(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
			Body:       io.NopCloser(strings.NewReader("image bytes")),
		}, nil
	})}, false, t.TempDir(), false)
	d.retryDelay = 0

	files, _, err := d.DownloadImagesConcurrently(
		context.Background(),
		[]string{"https://i1.mangakatana.com/token/abc/0.jpg"},
		filepath.Join(t.TempDir(), "chapter"),
		"https://mangakatana.com/manga/title/c50",
		1,
		noProgress{},
	)
	if err != nil {
		t.Fatalf("DownloadImagesConcurrently() error = %v", err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0], "page_001.jpg") {
		t.Fatalf("files = %#v", files)
	}
}

func TestImageExtIgnoresURLQuery(t *testing.T) {
	got := imageExt("https://cdn.test/page.webp?v=1781373584")
	if got != ".webp" {
		t.Fatalf("imageExt() = %q", got)
	}
}

type noProgress struct{}

func (noProgress) Update(int, int, int64) {}
func (noProgress) MarkDone()              {}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
