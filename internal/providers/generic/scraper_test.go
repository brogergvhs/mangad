package generic

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/brogergvhs/mangad/internal/ui"
)

func TestFetchBodyCloudflareUsesBrowserSolver(t *testing.T) {
	scraper := NewScraper(cfClient(), ui.NewLogger(false), nil, false, fakeBrowserFetcher("<html>solved</html>"))
	body, err := scraper.fetchBody(context.Background(), "http://manga.test")
	if err != nil {
		t.Fatal(err)
	}
	if body != "<html>solved</html>" {
		t.Fatalf("body = %q", body)
	}
}

func TestFetchBodyCloudflareWithoutBrowserSolver(t *testing.T) {
	scraper := NewScraper(cfClient(), ui.NewLogger(false), nil, false, nil)
	if _, err := scraper.fetchBody(context.Background(), "http://manga.test"); err == nil {
		t.Fatal("fetchBody() error = nil")
	} else if !strings.Contains(err.Error(), "browser_solver.enabled") {
		t.Fatalf("fetchBody() error = %v", err)
	}
}

func TestFetchBodyHTTPErrorUsesBrowserSolver(t *testing.T) {
	scraper := NewScraper(statusClient(http.StatusInternalServerError, "500 Internal Server Error"), ui.NewLogger(false), nil, false, fakeBrowserFetcher("<html>solved</html>"))
	body, err := scraper.fetchBody(context.Background(), "http://manga.test")
	if err != nil {
		t.Fatal(err)
	}
	if body != "<html>solved</html>" {
		t.Fatalf("body = %q", body)
	}
}

func TestFetchBodyHTTPErrorWithoutBrowserSolver(t *testing.T) {
	scraper := NewScraper(statusClient(http.StatusInternalServerError, "500 Internal Server Error"), ui.NewLogger(false), nil, false, nil)
	if _, err := scraper.fetchBody(context.Background(), "http://manga.test"); err == nil {
		t.Fatal("fetchBody() error = nil")
	} else if !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("fetchBody() error = %v", err)
	}
}

func TestGetImagesFetchesChapterPageOnce(t *testing.T) {
	var calls int
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body: io.NopCloser(bytes.NewBufferString(`
				<html><body><img src="https://cdn.test/page-001.webp"></body></html>
			`)),
			Header: http.Header{},
		}, nil
	})}

	scraper := NewScraper(client, ui.NewLogger(false), []string{"webp"}, false, nil)
	images, err := scraper.GetImages(context.Background(), "http://manga.test/chapter-1")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want 1", calls)
	}
	if len(images) != 1 || images[0] != "https://cdn.test/page-001.webp" {
		t.Fatalf("images = %#v", images)
	}
}

type fakeBrowserFetcher string

func (f fakeBrowserFetcher) LoadCached(context.Context, string) {}

func (f fakeBrowserFetcher) Fetch(context.Context, string) (string, error) {
	return string(f), nil
}

func statusClient(code int, status string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: code,
			Status:     status,
			Body:       io.NopCloser(bytes.NewBufferString(status)),
			Header:     http.Header{},
		}, nil
	})}
}

func cfClient() *http.Client {
	return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Status:     "403 Forbidden",
			Body:       io.NopCloser(bytes.NewBufferString("Just a moment")),
			Header:     http.Header{},
		}, nil
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
