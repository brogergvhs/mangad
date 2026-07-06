// Package browserfetch fetches pages through external browser solver services.
package browserfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	ProviderFlareSolverr        = "flaresolverr"
	DefaultFlareSolverrEndpoint = "http://localhost:8191/v1"
)

// Result is a browser-solved page response.
type Result struct {
	URL       string
	Status    int
	HTML      string
	UserAgent string
	Cookies   []*http.Cookie
}

// FlareSolverr calls a FlareSolverr /v1 endpoint.
type FlareSolverr struct {
	endpoint string
	client   *http.Client
	timeout  time.Duration
}

// NewFlareSolverr creates a FlareSolverr client.
func NewFlareSolverr(endpoint string, timeout time.Duration, client *http.Client) *FlareSolverr {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultFlareSolverrEndpoint
	}
	if timeout <= 0 {
		timeout = time.Minute
	}
	if client == nil {
		client = &http.Client{}
	}
	return &FlareSolverr{endpoint: endpoint, client: client, timeout: timeout}
}

// Fetch returns the browser-rendered HTML for target.
func (f *FlareSolverr) Fetch(ctx context.Context, target string) (Result, error) {
	var resp flaresolverrResponse
	if err := f.call(ctx, map[string]any{
		"cmd":        "request.get",
		"url":        target,
		"maxTimeout": int(f.timeout.Milliseconds()),
	}, &resp); err != nil {
		return Result{}, err
	}
	return Result{
		URL:       resp.Solution.URL,
		Status:    resp.Solution.Status,
		HTML:      resp.Solution.Response,
		UserAgent: resp.Solution.UserAgent,
		Cookies:   resp.httpCookies(),
	}, nil
}

// Health checks that the FlareSolverr endpoint accepts commands.
func (f *FlareSolverr) Health(ctx context.Context) error {
	var resp flaresolverrResponse
	return f.call(ctx, map[string]any{"cmd": "sessions.list"}, &resp)
}

func (f *FlareSolverr) call(ctx context.Context, payload map[string]any, out *flaresolverrResponse) error {
	// The solver may use the full maxTimeout budget; leave headroom to
	// transfer and decode the solved HTML.
	ctx, cancel := context.WithTimeout(ctx, f.timeout+15*time.Second)
	defer cancel()

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("call flaresolverr: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(readSnippet(resp.Body, 512))
		if msg != "" {
			msg = flaresolverrMessage(msg)
			return fmt.Errorf("flaresolverr http status %s: %s", resp.Status, msg)
		}
		return fmt.Errorf("flaresolverr http status %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode flaresolverr response: %w", err)
	}
	if out.Status != "ok" {
		msg := out.Message
		if msg == "" {
			msg = out.Status
		}
		return fmt.Errorf("flaresolverr: %s", msg)
	}
	return nil
}

func readSnippet(r io.Reader, limit int64) string {
	if r == nil || limit <= 0 {
		return ""
	}
	data, _ := io.ReadAll(io.LimitReader(r, limit))
	return string(data)
}

func flaresolverrMessage(body string) string {
	var resp struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err == nil && strings.TrimSpace(resp.Message) != "" {
		return strings.TrimSpace(resp.Message)
	}
	return body
}

type flaresolverrResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		URL       string `json:"url"`
		Status    int    `json:"status"`
		Response  string `json:"response"`
		UserAgent string `json:"userAgent"`
		Cookies   []struct {
			Name     string  `json:"name"`
			Value    string  `json:"value"`
			Domain   string  `json:"domain"`
			Path     string  `json:"path"`
			Expires  float64 `json:"expires"`
			HTTPOnly bool    `json:"httpOnly"`
			Secure   bool    `json:"secure"`
		} `json:"cookies"`
	} `json:"solution"`
}

func (r flaresolverrResponse) httpCookies() []*http.Cookie {
	out := make([]*http.Cookie, 0, len(r.Solution.Cookies))
	for _, c := range r.Solution.Cookies {
		if c.Name == "" {
			continue
		}
		cookie := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			HttpOnly: c.HTTPOnly,
			Secure:   c.Secure,
		}
		if c.Expires > 0 {
			cookie.Expires = time.Unix(int64(c.Expires), 0)
		}
		out = append(out, cookie)
	}
	return out
}
