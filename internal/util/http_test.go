package util

import (
	"net/http"
	"testing"
	"time"
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

func TestHTTPClientRateLimitsPerHost(t *testing.T) {
	t.Parallel()

	client, err := NewHTTPClient(HTTPClientOptions{
		RateLimit: HostRateLimit{Interval: 25 * time.Millisecond, Burst: 1},
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	if _, err := client.Get("http://rate-limit.test/1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get("http://rate-limit.test/2"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("two requests took %s, want rate-limited delay", elapsed)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
