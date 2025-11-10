package generic

import (
	"context"
	"encoding/json"
	"fmt"
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

func (s *Scraper) probeDynamicEndpoints(
	ctx context.Context,
	chapterURL string,
	js JSAnalysis,
	col *imageCollector,
) {

	candidates := tryBuildDynamicURLs(js)
	if s.debug {
		fmt.Println("[debug] Dynamic endpoint candidates:", candidates)
	}

	for _, path := range candidates {
		fullURL := resolve(chapterURL, path)

		if s.debug {
			fmt.Println("[debug] Probing dynamic:", fullURL)
		}

		html, ok := s.tryDynamicFetch(ctx, fullURL, "POST")
		if !ok {
			html, ok = s.tryDynamicFetch(ctx, fullURL, "GET")
		}

		if !ok {
			continue
		}

		var obj map[string]any
		if json.Unmarshal([]byte(html), &obj) == nil {
			col.ScanNuxt(obj, chapterURL)
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
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	return string(b), true
}
