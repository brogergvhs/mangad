package service

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brogergvhs/mangad/internal/browserfetch"
	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/providers"
	"github.com/brogergvhs/mangad/internal/util"
)

func TestFlareSolverrFetcherAppliesSession(t *testing.T) {
	state := &util.BrowserState{}
	var gotUA, gotCookie string
	client, err := util.NewHTTPClient(util.HTTPClientOptions{
		UserAgent: "initial",
		State:     state,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotUA = r.UserAgent()
			gotCookie = r.Header.Get("Cookie")
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	flaresolverrFetcher{
		http:  client,
		state: state,
		cache: browserCookieCache{dbPath: filepath.Join(t.TempDir(), "mangad.db")},
	}.applySession(context.Background(), "https://manga.test/chapter", browserfetch.Result{
		URL:       "https://manga.test/chapter",
		UserAgent: "solver",
		Cookies: []*http.Cookie{{
			Name:   "cf_clearance",
			Value:  "token",
			Domain: ".manga.test",
			Path:   "/",
			Secure: true,
		}},
	})

	if _, err := client.Get("https://img.manga.test/page.webp"); err != nil {
		t.Fatal(err)
	}
	if gotUA != "solver" {
		t.Fatalf("User-Agent = %q", gotUA)
	}
	if gotCookie != "cf_clearance=token" {
		t.Fatalf("Cookie = %q", gotCookie)
	}
}

func TestDownloadSummaryIncludesChapterErrors(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "2.jpg") {
			return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Body: http.NoBody}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"image/jpeg"}},
			Body:       io.NopCloser(strings.NewReader("jpg")),
		}, nil
	})}
	svc := NewDownloadService(
		&config.Config{Output: t.TempDir(), ImageWorkers: 1, ChapterWorkers: 1},
		client,
		fakeScraper{images: []string{"https://cdn.test/1.jpg", "https://cdn.test/2.jpg"}},
		noopLogger{},
		nil,
	)

	summary, err := svc.Download(context.Background(), []chapters.Chapter{{Chapter: providers.Chapter{
		URL:   "https://manga.test/chapter-1",
		Title: "Chapter 1",
		Label: "1",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.FailedChapters != 1 || len(summary.Errors) != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if !strings.Contains(summary.Errors[0], "image 2: HTTP 403") {
		t.Fatalf("summary errors = %#v", summary.Errors)
	}
}

type fakeScraper struct {
	images []string
}

func (f fakeScraper) GetChapters(context.Context, string) ([]providers.Chapter, error) {
	return nil, nil
}

func (f fakeScraper) GetImages(context.Context, string) ([]string, error) {
	return f.images, nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
