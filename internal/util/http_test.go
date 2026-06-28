package util

import (
	"net/http"
	"testing"
)

func TestBrowserStateUserAgent(t *testing.T) {
	state := &BrowserState{}
	var got string
	client, err := NewHTTPClient(HTTPClientOptions{
		UserAgent: "initial",
		State:     state,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			got = r.UserAgent()
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Get("http://manga.test"); err != nil {
		t.Fatal(err)
	}
	if got != "initial" {
		t.Fatalf("User-Agent = %q", got)
	}

	state.SetUserAgent("solver")
	if _, err := client.Get("http://manga.test"); err != nil {
		t.Fatal(err)
	}
	if got != "solver" {
		t.Fatalf("User-Agent = %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
