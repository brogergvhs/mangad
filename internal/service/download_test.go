package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brogergvhs/kaodoku/internal/browserfetch"
	"github.com/brogergvhs/kaodoku/internal/chapters"
	"github.com/brogergvhs/kaodoku/internal/config"
	"github.com/brogergvhs/kaodoku/internal/providers"
	"github.com/brogergvhs/kaodoku/internal/ui"
	"github.com/brogergvhs/kaodoku/internal/util"
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
		cache: browserCookieCache{dbPath: filepath.Join(t.TempDir(), "kaodoku.db")},
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
		ui.Discard(),
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

func TestDownloadUsesBrowserDownloaderFallback(t *testing.T) {
	worker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Kaodoku-Images", "7")
		_, _ = w.Write([]byte("cbz"))
	}))
	defer worker.Close()

	output := t.TempDir()
	svc := NewDownloadService(
		&config.Config{
			Output:         output,
			ImageWorkers:   1,
			ChapterWorkers: 1,
			AllowExt:       []string{"webp"},
			BrowserDownload: config.BrowserDownloadConfig{
				Enabled:        true,
				Endpoint:       worker.URL,
				TimeoutSeconds: 1,
			},
		},
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Body: http.NoBody}, nil
		})},
		fakeScraper{images: []string{"https://cdn.test/1.webp"}},
		ui.Discard(),
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
	if summary.FailedChapters != 0 || summary.Chapters != 1 || summary.Images != 7 {
		t.Fatalf("summary = %#v", summary)
	}
	if _, err := os.Stat(filepath.Join(output, "1_chapter_1.cbz")); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadCleansTempFolderOnCBZFailure(t *testing.T) {
	output := t.TempDir()
	cbzPath := filepath.Join(output, "1_chapter_1.cbz")
	if err := os.Mkdir(cbzPath, 0755); err != nil {
		t.Fatal(err)
	}

	svc := NewDownloadService(
		&config.Config{Output: output, ImageWorkers: 1, ChapterWorkers: 1},
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"image/jpeg"}},
				Body:       io.NopCloser(strings.NewReader("jpg")),
			}, nil
		})},
		fakeScraper{images: []string{"https://cdn.test/1.jpg"}},
		ui.Discard(),
		nil,
	)

	_, err := svc.DownloadChapter(context.Background(), chapters.Chapter{Chapter: providers.Chapter{
		URL:   "https://manga.test/chapter-1",
		Title: "Chapter 1",
		Label: "1",
	}})
	if err == nil {
		t.Fatal("DownloadChapter() error = nil")
	}
	if _, err := os.Stat(filepath.Join(output, "1_chapter_1_tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp folder still exists or stat failed: %v", err)
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

func TestFetchChaptersChapterless(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Chapterless = true
	svc, err := NewSourceDownloadService(cfg, ui.NewLogger(false), nil, "generic")
	if err != nil {
		t.Fatal(err)
	}
	list, finalURL, gap, err := svc.FetchChapters(context.Background(), "https://gallery.test/g/42")
	if err != nil || gap != 0 || finalURL != "https://gallery.test/g/42" {
		t.Fatalf("err=%v gap=%d final=%s", err, gap, finalURL)
	}
	if len(list) != 1 || list[0].Label != "Oneshot" || list[0].URL != "https://gallery.test/g/42" {
		t.Fatalf("list = %#v", list)
	}
}
