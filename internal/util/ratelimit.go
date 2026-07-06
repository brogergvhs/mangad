package util

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// HostRateLimit controls per-host HTTP pacing. Zero values fall back to the
// built-in default; set Disabled to opt out entirely.
type HostRateLimit struct {
	Interval time.Duration
	Burst    int
	Disabled bool
}

var defaultHostRateLimit = HostRateLimit{Interval: 200 * time.Millisecond, Burst: 2}

// hostLimiters paces requests per hostname for one HTTP client.
type hostLimiters struct {
	cfg HostRateLimit

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

func newHostLimiters(cfg HostRateLimit) *hostLimiters {
	if cfg.Disabled {
		return nil
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultHostRateLimit.Interval
	}
	if cfg.Burst <= 0 {
		cfg.Burst = defaultHostRateLimit.Burst
	}
	return &hostLimiters{cfg: cfg, limiters: map[string]*rate.Limiter{}}
}

// Wait blocks until a request to host may proceed or ctx is done.
// A nil receiver never blocks.
func (h *hostLimiters) Wait(ctx context.Context, host string) error {
	if h == nil {
		return nil
	}
	host = rateLimitHost(host)
	if host == "" {
		return nil
	}

	h.mu.Lock()
	limiter := h.limiters[host]
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Every(h.cfg.Interval), h.cfg.Burst)
		h.limiters[host] = limiter
	}
	h.mu.Unlock()

	return limiter.Wait(ctx)
}

func rateLimitHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, "[]")
}
