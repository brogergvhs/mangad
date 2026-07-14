package util

import (
	"context"
	"testing"
	"time"
)

// An exempted context (image downloads) must never wait on the limiter, even
// with the host's burst fully consumed.
func TestRateLimitExemptContextSkipsPacing(t *testing.T) {
	t.Parallel()
	h := newHostLimiters(HostRateLimit{Interval: time.Hour, Burst: 1})
	if err := h.Wait(context.Background(), "site.test"); err != nil { // consumes the burst
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Wait(ExemptFromRateLimit(context.Background()), "site.test") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exempt request blocked on the limiter")
	}
}
