package util

import (
	"context"
	"errors"
	"net/http"
	"strings"
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

func TestHTTPClientBlocksPrivateNetworks(t *testing.T) {
	t.Parallel()

	var called bool
	client, err := NewHTTPClient(HTTPClientOptions{
		BlockPrivateNetworks: true,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"http://127.0.0.1/", "http://10.0.0.1/", "http://[::1]/", "http://localhost/"} {
		called = false
		if _, err := client.Get(target); err == nil || !strings.Contains(err.Error(), "private network") {
			t.Fatalf("GET %s err = %v, want private network block", target, err)
		}
		if called {
			t.Fatalf("transport called for blocked target %s", target)
		}
	}
}

func TestHTTPClientAllowsPublicLiteral(t *testing.T) {
	t.Parallel()

	var called bool
	client, err := NewHTTPClient(HTTPClientOptions{
		BlockPrivateNetworks: true,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get("http://93.184.216.34/")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !called {
		t.Fatal("transport was not called for public target")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestDoWithRetryStatusHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statuses   []int
		wantCalls  int
		wantStatus int // response status when returned; 0 means error expected
		wantErr    int // StatusError code; 0 means nil error
	}{
		{name: "retries 429 then succeeds", statuses: []int{429, 200}, wantCalls: 2, wantStatus: 200},
		{name: "retries 500 then succeeds", statuses: []int{500, 200}, wantCalls: 2, wantStatus: 200},
		{name: "404 fails without retry", statuses: []int{404}, wantCalls: 1, wantErr: 404},
		{name: "403 returned for inspection", statuses: []int{403}, wantCalls: 1, wantStatus: 403},
		{name: "exhausted retries return status error", statuses: []int{500, 500, 500}, wantCalls: 3, wantErr: 500},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				status := tc.statuses[calls]
				calls++
				return &http.Response{StatusCode: status, Body: http.NoBody, Header: http.Header{}}, nil
			})}
			req, err := http.NewRequest(http.MethodGet, "http://retry.test/", nil)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := DoWithRetry(client, req, 3, time.Millisecond)
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
			if tc.wantErr != 0 {
				var statusErr *StatusError
				if !errors.As(err, &statusErr) || statusErr.Code != tc.wantErr {
					t.Fatalf("err = %v, want StatusError %d", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
		})
	}
}

func TestDoWithRetryHonorsContextCancel(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: http.NoBody, Header: http.Header{}}, nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://retry.test/", nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := DoWithRetry(client, req, 3, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
