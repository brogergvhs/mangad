package comickz

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/brogergvhs/kaodoku/internal/ui"
)

func TestGetChaptersFetchesPaginatedChapterAPI(t *testing.T) {
	var calls []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.String())
		body := `{}`
		if req.URL.Path == "/api/comics/title-slug/chapter-list" {
			if req.Header.Get("Accept") != "application/json" {
				t.Fatalf("Accept = %q", req.Header.Get("Accept"))
			}
			switch req.URL.Query().Get("page") {
			case "1":
				body = `{
					"data": [
						{"hid":"c","chap":"3","title":"","lang":"en"},
						{"hid":"b","chap":"2","title":null,"lang":"en"}
					],
					"pagination":{"current_page":1,"last_page":2,"total":3}
				}`
			case "2":
				body = `{
					"data": [
						{"hid":"a","chap":"1","title":"Start","lang":"en"}
					],
					"pagination":{"current_page":2,"last_page":2,"total":3}
				}`
			default:
				t.Fatalf("unexpected page query: %q", req.URL.RawQuery)
			}
		}
		return response(body), nil
	})}

	scraper := NewScraper(client, ui.NewLogger(false), nil, false, nil)
	chapters, err := scraper.GetChapters(context.Background(), "https://comickz.co.uk/comic/title-slug")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 3 {
		t.Fatalf("chapters = %#v", chapters)
	}
	if chapters[0].Label != "1" || chapters[1].Label != "2" || chapters[2].Label != "3" {
		t.Fatalf("chapters = %#v", chapters)
	}
	if chapters[0].Title != "Chapter 1 - Start" {
		t.Fatalf("chapter title = %q", chapters[0].Title)
	}
	if chapters[0].URL != "https://comickz.co.uk/comic/title-slug/a-chapter-1-en" {
		t.Fatalf("chapter URL = %q", chapters[0].URL)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "/api/comics/title-slug/chapter-list?lang=en&page=2") {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestGetChaptersDedupesDuplicateGroups(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return response(`{
			"data": [
				{"hid":"a","chap":"10","title":"","lang":"en"},
				{"hid":"b","chap":"10","title":"","lang":"en"},
				{"hid":"c","chap":"9.5","title":"","lang":"en"}
			],
			"pagination":{"current_page":1,"last_page":1,"total":3}
		}`), nil
	})}

	scraper := NewScraper(client, ui.NewLogger(false), nil, false, nil)
	chapters, err := scraper.GetChapters(context.Background(), "https://comickz.co.uk/comic/title-slug")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 2 {
		t.Fatalf("chapters = %#v", chapters)
	}
	if chapters[0].Label != "9.5" || chapters[1].Label != "10" {
		t.Fatalf("chapters = %#v", chapters)
	}
}

func response(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestSearchMangaHitsAPI(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/api/search" || req.URL.Query().Get("q") != "demonic sword" {
			t.Fatalf("unexpected request %s", req.URL)
		}
		body := `{"data":[{"slug":"the-supreme-demonic-sword"},{"slug":"another-one"},{"slug":""}]}`
		return response(body), nil
	})}
	scraper := NewScraper(client, ui.NewLogger(false), nil, false, nil)
	urls, err := scraper.SearchManga(context.Background(), "https://comickz.co.uk/search?q=demonic+sword", "demonic sword")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://comickz.co.uk/comic/the-supreme-demonic-sword",
		"https://comickz.co.uk/comic/another-one",
	}
	if len(urls) != len(want) {
		t.Fatalf("urls = %#v", urls)
	}
	for i := range want {
		if urls[i] != want[i] {
			t.Fatalf("urls[%d] = %q, want %q", i, urls[i], want[i])
		}
	}
}
