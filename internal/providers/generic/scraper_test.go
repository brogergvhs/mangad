package generic

import (
	"bytes"
	"context"
	"io"
	"net/http"
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
	}
}

type fakeBrowserFetcher string

func (f fakeBrowserFetcher) Fetch(context.Context, string) (string, error) {
	return string(f), nil
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
