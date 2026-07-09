package madara

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/brogergvhs/mangad/internal/ui"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGetChaptersUsesAjaxEndpoint(t *testing.T) {
	ajax := `<ul><li><a href="https://manga.test/manga/demo/chapter-1/">Chapter 1</a></li>
		<li><a href="https://manga.test/manga/demo/chapter-2/">Chapter 2</a></li></ul>`
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/ajax/chapters/") {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(ajax)), Header: http.Header{}}, nil
	})}
	scraper := NewScraper(client, ui.NewLogger(false), nil, false, nil)
	chapters, err := scraper.GetChapters(context.Background(), "https://manga.test/manga/demo/")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 2 || chapters[0].Label != "1" {
		t.Fatalf("chapters = %#v", chapters)
	}
}

func TestGetChaptersFallsBackToGeneric(t *testing.T) {
	page := `<html><body><a href="https://manga.test/manga/demo/chapter-5/">Chapter 5</a></body></html>`
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewBufferString("")), Header: http.Header{}}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(page)), Header: http.Header{}}, nil
	})}
	scraper := NewScraper(client, ui.NewLogger(false), nil, false, nil)
	chapters, err := scraper.GetChapters(context.Background(), "https://manga.test/manga/demo/")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 1 || chapters[0].Label != "5" {
		t.Fatalf("chapters = %#v", chapters)
	}
}
