// Package browserfetch fetches pages through external browser solver services.
package browserfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	}, nil
}

// Health checks that the FlareSolverr endpoint accepts commands.
func (f *FlareSolverr) Health(ctx context.Context) error {
	var resp flaresolverrResponse
	return f.call(ctx, map[string]any{"cmd": "sessions.list"}, &resp)
}

func (f *FlareSolverr) call(ctx context.Context, payload map[string]any, out *flaresolverrResponse) error {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
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

type flaresolverrResponse struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		URL       string `json:"url"`
		Status    int    `json:"status"`
		Response  string `json:"response"`
		UserAgent string `json:"userAgent"`
	} `json:"solution"`
}
