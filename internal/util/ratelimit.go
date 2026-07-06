package util

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// HostRateLimit controls per-host HTTP pacing.
type HostRateLimit struct {
	Interval time.Duration
	Burst    int
	Disabled bool
}

var defaultHostRateLimit = HostRateLimit{Interval: 200 * time.Millisecond, Burst: 2}

var (
	hostLimitersMu sync.Mutex
	hostLimiters   = map[string]*rateLimiter{}
)

type rateLimiter struct {
	tokens chan struct{}
}

func waitHost(ctx context.Context, host string, cfg HostRateLimit) error {
	host = rateLimitHost(host)
	if host == "" || cfg.Disabled {
		return nil
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultHostRateLimit.Interval
	}
	if cfg.Burst <= 0 {
		cfg.Burst = defaultHostRateLimit.Burst
	}
	limiter := getHostLimiter(host, cfg)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-limiter.tokens:
		return nil
	}
}

func rateLimitHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.Trim(value, "[]")
}

func getHostLimiter(host string, cfg HostRateLimit) *rateLimiter {
	hostLimitersMu.Lock()
	defer hostLimitersMu.Unlock()
	if existing := hostLimiters[host]; existing != nil {
		return existing
	}
	created := newRateLimiter(cfg)
	hostLimiters[host] = created
	return created
}

func newRateLimiter(cfg HostRateLimit) *rateLimiter {
	limiter := &rateLimiter{tokens: make(chan struct{}, cfg.Burst)}
	for i := 0; i < cfg.Burst; i++ {
		limiter.tokens <- struct{}{}
	}
	go func() {
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for range ticker.C {
			select {
			case limiter.tokens <- struct{}{}:
			default:
			}
		}
	}()
	return limiter
}
