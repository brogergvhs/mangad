package util

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type HTTPClientOptions struct {
	Timeout     time.Duration
	UserAgent   string
	Cookie      string
	CookieFile  string
	Transport   http.RoundTripper
	State       *BrowserState
	RateLimit   HostRateLimit
	DebugLogger interface {
		Debugf(string, ...any)
	}
}

type BrowserState struct {
	mu        sync.RWMutex
	userAgent string
}

func (s *BrowserState) SetUserAgent(value string) {
	if s == nil || strings.TrimSpace(value) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userAgent = value
}

func (s *BrowserState) UserAgent() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userAgent
}

func NewHTTPClient(opts HTTPClientOptions) (*http.Client, error) {
	jar, _ := cookiejar.New(nil)

	var baseTransport http.RoundTripper
	if opts.Transport != nil {
		baseTransport = opts.Transport
	} else {
		baseTransport = &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DisableCompression:  false,
			MaxIdleConns:        100,
			MaxConnsPerHost:     100,
			MaxIdleConnsPerHost: 100,
			ForceAttemptHTTP2:   true,
		}
	}

	client := &http.Client{
		Timeout: opts.Timeout,
		Transport: roundTripper{
			base:         baseTransport,
			ua:           opts.UserAgent,
			cookieHeader: joinCookies(opts.Cookie, opts.CookieFile),
			state:        opts.State,
			limiters:     newHostLimiters(opts.RateLimit),
			log:          opts.DebugLogger,
		},
		Jar: jar,
	}

	if opts.DebugLogger != nil {
		opts.DebugLogger.Debugf("HTTP client initialized (timeout=%s, ua=%q, cookieFile=%q)\n",
			opts.Timeout, opts.UserAgent, opts.CookieFile)
	}

	return client, nil
}

type roundTripper struct {
	base         http.RoundTripper
	ua           string
	cookieHeader string
	state        *BrowserState
	limiters     *hostLimiters
	log          interface{ Debugf(string, ...any) }
}

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTrippers must not modify the caller's request.
	req = req.Clone(req.Context())

	ua := rt.ua
	if rt.state != nil {
		if stateUA := rt.state.UserAgent(); stateUA != "" {
			ua = stateUA
		}
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	if rt.cookieHeader != "" {
		if req.Header.Get("Cookie") == "" {
			req.Header.Set("Cookie", rt.cookieHeader)
		}
	}

	if rt.log != nil {
		rt.log.Debugf("HTTP %s %s", req.Method, req.URL.String())
	}
	if err := rt.limiters.Wait(req.Context(), req.URL.Host); err != nil {
		return nil, err
	}

	return rt.base.RoundTrip(req)
}

func joinCookies(inline, file string) string {
	s := strings.TrimSpace(inline)
	if file != "" {
		if b, err := os.ReadFile(file); err == nil {
			// first non-empty line
			sc := bufio.NewScanner(strings.NewReader(string(b)))
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line != "" {
					if s == "" {
						s = line
					} else {
						s = s + "; " + line
					}
					break
				}
			}
		}
	}

	return s
}

// StatusError reports a non-success HTTP status after retries.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("HTTP %d", e.Code) }

// DoWithRetry executes request, retrying transport errors, 429s, and 5xx
// responses with linear backoff (429 also honors Retry-After). Responses
// below 400 — and 403s, whose body callers inspect for challenge pages —
// are returned as-is; any other status yields a *StatusError.
func DoWithRetry(c *http.Client, req *http.Request, attempts int, backoff time.Duration) (*http.Response, error) {
	var lastErr error

	for i := 1; i <= attempts; i++ {
		var retryAfter time.Duration
		resp, err := c.Do(req)
		if err != nil {
			lastErr = err
		} else {
			switch {
			case resp.StatusCode < http.StatusBadRequest || resp.StatusCode == http.StatusForbidden:
				return resp, nil
			case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError:
				lastErr = &StatusError{Code: resp.StatusCode}
				retryAfter = retryAfterDelay(resp)
				_ = resp.Body.Close()
			default:
				_ = resp.Body.Close()
				return nil, &StatusError{Code: resp.StatusCode}
			}
		}

		if i == attempts {
			break
		}
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(max(backoff*time.Duration(i), retryAfter)):
		}
	}

	return nil, lastErr
}

func retryAfterDelay(resp *http.Response) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || seconds <= 0 {
		return 0
	}
	const maxRetryAfter = 30 * time.Second
	return min(time.Duration(seconds)*time.Second, maxRetryAfter)
}

func PickUserAgent(override string) string {
	if override != "" {
		return override
	}

	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
}
