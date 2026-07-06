package util

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
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
			rateLimit:    opts.RateLimit,
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
	rateLimit    HostRateLimit
	log          interface{ Debugf(string, ...any) }
}

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
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
	if err := waitHost(req.Context(), req.URL.Host, rt.rateLimit); err != nil {
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

// DoWithRetry executes request with simple retry policy.
func DoWithRetry(c *http.Client, req *http.Request, attempts int, backoff time.Duration) (*http.Response, error) {
	var resp *http.Response
	var err error

	for i := 1; i <= attempts; i++ {
		resp, err = c.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 500 {
			return resp, nil
		}

		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}

		if i == attempts {
			break
		}
		time.Sleep(backoff * time.Duration(i))
	}

	if err == nil && resp != nil {
		return resp, fmt.Errorf("HTTP %d after %d attempts", resp.StatusCode, attempts)
	}

	return nil, err
}

func PickUserAgent(override string) string {
	if override != "" {
		return override
	}

	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
}
