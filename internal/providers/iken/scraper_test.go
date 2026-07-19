package iken

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/brogergvhs/kaodoku/internal/ui"
)

func TestGetChaptersFetchesFullListFromAPI(t *testing.T) {
	var calls []string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls = append(calls, req.URL.String())
		if req.URL.Host != "api.vortexscans.org" {
			t.Fatalf("unexpected host %s", req.URL.Host)
		}
		switch req.URL.Path {
		case "/api/post":
			if req.URL.Query().Get("postSlug") != "some-series" {
				t.Fatalf("postSlug = %q", req.URL.Query().Get("postSlug"))
			}
			return response(`{"totalChapterCount":4,"post":{"id":236}}`), nil
		case "/api/chapters":
			if req.URL.Query().Get("postId") != "236" {
				t.Fatalf("postId = %q", req.URL.Query().Get("postId"))
			}
			return response(`{"post":{"chapters":[
				{"slug":"chapter-145","number":145,"title":"","isAccessible":false},
				{"slug":"chapter-144","number":144,"title":"Finale","isAccessible":true},
				{"slug":"chapter-9-5","number":9.5,"title":"","isAccessible":true},
				{"slug":"chapter-0","number":0,"title":"","isAccessible":true}
			]},"totalChapterCount":4}`), nil
		}
		t.Fatalf("unexpected path %s", req.URL.Path)
		return nil, nil
	})}

	scraper := NewScraper(client, ui.NewLogger(false), nil, false, nil)
	chapters, err := scraper.GetChapters(t.Context(), "https://vortexscans.org/series/some-series")
	if err != nil {
		t.Fatal(err)
	}
	// The locked chapter 145 is skipped; the rest come back sorted ascending.
	if len(chapters) != 3 {
		t.Fatalf("chapters = %#v", chapters)
	}
	if chapters[0].Label != "0" || chapters[1].Label != "9.5" || chapters[2].Label != "144" {
		t.Fatalf("chapters = %#v", chapters)
	}
	if chapters[2].Title != "Chapter 144 - Finale" {
		t.Fatalf("title = %q", chapters[2].Title)
	}
	if chapters[2].URL != "https://vortexscans.org/series/some-series/chapter-144" {
		t.Fatalf("URL = %q", chapters[2].URL)
	}
	if chapters[1].NumMain != 9 || chapters[1].SuffixNum != 5 {
		t.Fatalf("9.5 sort key = %#v", chapters[1])
	}
	if !strings.Contains(strings.Join(calls, "\n"), "take=200") {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestGetChaptersFallsBackToSameHostAPI(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "api.example.org" {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(bytes.NewBufferString(""))}, nil
		}
		switch req.URL.Path {
		case "/api/post":
			return response(`{"totalChapterCount":1,"post":{"id":7}}`), nil
		case "/api/chapters":
			return response(`{"post":{"chapters":[{"slug":"chapter-1","number":1,"isAccessible":true}]},"totalChapterCount":1}`), nil
		}
		t.Fatalf("unexpected path %s", req.URL.Path)
		return nil, nil
	})}

	scraper := NewScraper(client, ui.NewLogger(false), nil, false, nil)
	chapters, err := scraper.GetChapters(t.Context(), "https://example.org/series/some-series")
	if err != nil {
		t.Fatal(err)
	}
	if len(chapters) != 1 || chapters[0].Label != "1" {
		t.Fatalf("chapters = %#v", chapters)
	}
}

func TestSearchMangaReturnsSiteURLs(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.vortexscans.org" || req.URL.Path != "/api/query" {
			t.Fatalf("unexpected request %s", req.URL)
		}
		if req.URL.Query().Get("searchTerm") != "earth game" {
			t.Fatalf("searchTerm = %q", req.URL.Query().Get("searchTerm"))
		}
		return response(`{"posts":[{"slug":"hardcore-leveling-warrior:-earth-game"},{"slug":""}]}`), nil
	})}
	scraper := NewScraper(client, ui.NewLogger(false), nil, false, nil)
	urls, err := scraper.SearchManga(t.Context(), "https://api.vortexscans.org/api/query?searchTerm={query}&perPage=10", "earth game")
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 1 || urls[0] != "https://vortexscans.org/series/hardcore-leveling-warrior:-earth-game" {
		t.Fatalf("urls = %#v", urls)
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
