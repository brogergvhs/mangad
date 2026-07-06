package generic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func tryBuildDynamicURLs(js JSAnalysis) []string {
	var results []string

	for _, base := range js.URLs {
		if strings.Contains(base, "chap") && strings.HasSuffix(base, "/") {
			for key, val := range js.Vars {
				if strings.Contains(strings.ToLower(key), "id") {
					results = append(results, base+val)
				}
			}
		}
	}

	results = append(results, js.Calls...)

	seen := map[string]bool{}
	final := []string{}
	for _, u := range results {
		if !seen[u] {
			seen[u] = true
			final = append(final, u)
		}
	}

	return final
}

const maxDynamicProbes = 10

func (s *Scraper) probeDynamicEndpoints(
	ctx context.Context,
	chapterURL string,
	js JSAnalysis,
	col *imageCollector,
) {
	candidates := tryBuildDynamicURLs(js)
	s.log.Debugf("Dynamic endpoint candidates: %v\n", candidates)
	if len(candidates) > maxDynamicProbes {
		s.log.Infof("Probing only %d of %d dynamic endpoint candidates.\n", maxDynamicProbes, len(candidates))
		candidates = candidates[:maxDynamicProbes]
	}

	for _, path := range candidates {
		fullURL := resolve(chapterURL, path)
		// Probe read-only and only the chapter's own host: these URLs are
		// mined from page scripts and can point anywhere.
		if !sameHost(chapterURL, fullURL) {
			s.log.Debugf("Skipping off-host dynamic candidate: %s\n", fullURL)
			continue
		}

		s.log.Debugf("Probing dynamic: %s\n", fullURL)

		html, ok := s.tryDynamicFetch(ctx, fullURL, http.MethodGet)
		if !ok {
			continue
		}

		if strings.HasPrefix(strings.TrimSpace(html), "{") {
			var obj map[string]any
			if err := json.Unmarshal([]byte(html), &obj); err == nil {
				col.ScanNuxt(obj, chapterURL)
			}
		}
	}
}

func (s *Scraper) tryDynamicFetch(
	ctx context.Context,
	url string,
	method string,
) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return "", false
	}

	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", false
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			s.log.Debugf("Warning: failed to close response body for %s: %v\n", url, cerr)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		s.log.Debugf("Warning: failed to read body from %s: %v\n", url, err)
		return "", false
	}

	return string(b), true
}
