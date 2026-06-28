package service

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/brogergvhs/mangad/internal/browserfetch"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
