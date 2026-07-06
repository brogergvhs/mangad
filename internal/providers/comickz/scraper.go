// Package comickz implements the Comickz-specific chapter-list API while
// reusing the generic image scraper.
package comickz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brogergvhs/mangad/internal/providers"
	"github.com/brogergvhs/mangad/internal/providers/generic"
	"github.com/brogergvhs/mangad/internal/ui"
	"github.com/brogergvhs/mangad/internal/util"
)

const maxChapterPages = 100

type Scraper struct {
	client   *http.Client
	log      ui.Log
	fallback providers.Scraper
}

func NewScraper(c *http.Client, log ui.Log, allowExt []string, checkJS bool, browser generic.BrowserFetcher) *Scraper {
	return &Scraper{
		client:   c,
		log:      log,
		fallback: generic.NewScraper(c, log, allowExt, checkJS, browser),
	}
}

func (s *Scraper) GetChapters(ctx context.Context, pageURL string) ([]providers.Chapter, error) {
	chapters, err := s.fetchAPIChapters(ctx, pageURL)
	if err == nil && len(chapters) > 0 {
		return chapters, nil
	}
	if err != nil && s.log != nil {
		s.log.Debugf("Comickz chapter API failed for %s: %v\n", pageURL, err)
	}
	return s.fallback.GetChapters(ctx, pageURL)
}

func (s *Scraper) GetImages(ctx context.Context, chapterURL string) ([]string, error) {
	return s.fallback.GetImages(ctx, chapterURL)
}

func (s *Scraper) fetchAPIChapters(ctx context.Context, pageURL string) ([]providers.Chapter, error) {
	slug, err := comicSlug(pageURL)
	if err != nil {
		return nil, err
	}

	var out []providers.Chapter
	lastPage := 1
	for page := 1; page <= lastPage && page <= maxChapterPages; page++ {
		resp, err := s.fetchChapterPage(ctx, pageURL, slug, page)
		if err != nil {
			return nil, err
		}
		if resp.Pagination.LastPage > lastPage {
			lastPage = resp.Pagination.LastPage
		}
		out = append(out, chaptersFromRows(pageURL, slug, resp.Data)...)
	}
	if lastPage > maxChapterPages && s.log != nil {
		s.log.Infof("Comickz chapter list for %s truncated at %d of %d pages.\n", pageURL, maxChapterPages, lastPage)
	}

	return dedupeAndSort(out), nil
}

type chapterListResponse struct {
	Data       []chapterRow `json:"data"`
	Pagination struct {
		CurrentPage int `json:"current_page"`
		LastPage    int `json:"last_page"`
		Total       int `json:"total"`
	} `json:"pagination"`
}

type chapterRow struct {
	HID   string `json:"hid"`
	Chap  string `json:"chap"`
	Title any    `json:"title"`
	Lang  string `json:"lang"`
}

func (s *Scraper) fetchChapterPage(ctx context.Context, pageURL, slug string, page int) (chapterListResponse, error) {
	apiURL, err := chapterAPIURL(pageURL, slug, page)
	if err != nil {
		return chapterListResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return chapterListResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", pageURL)

	resp, err := util.DoWithRetry(s.client, req, 3, 500*time.Millisecond)
	if err != nil {
		return chapterListResponse{}, err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && s.log != nil {
			s.log.Debugf("Warning: failed to close response body: %v\n", cerr)
		}
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return chapterListResponse{}, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return chapterListResponse{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var out chapterListResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return chapterListResponse{}, err
	}
	return out, nil
}

func comicSlug(pageURL string) (string, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return "", err
	}
	parts := strings.FieldsFunc(strings.Trim(u.Path, "/"), func(r rune) bool { return r == '/' })
	if len(parts) < 2 || parts[0] != "comic" {
		return "", fmt.Errorf("not a Comickz comic URL")
	}
	return parts[1], nil
}

func chapterAPIURL(pageURL, slug string, page int) (string, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return "", err
	}
	u.Path = fmt.Sprintf("/api/comics/%s/chapter-list", slug)
	u.RawQuery = url.Values{
		"lang": []string{"en"},
		"page": []string{strconv.Itoa(page)},
	}.Encode()
	return u.String(), nil
}

func chaptersFromRows(pageURL, slug string, rows []chapterRow) []providers.Chapter {
	out := make([]providers.Chapter, 0, len(rows))
	for _, row := range rows {
		hid := strings.TrimSpace(row.HID)
		chap := strings.TrimSpace(row.Chap)
		lang := strings.TrimSpace(row.Lang)
		if hid == "" || chap == "" || lang == "" {
			continue
		}
		main, suffixType, suffixNum, label, ok := parseChapter(chap)
		if !ok {
			continue
		}
		title := "Chapter " + label
		if extra := stringValue(row.Title); extra != "" {
			title += " - " + extra
		}
		out = append(out, providers.Chapter{
			URL:        providers.ResolveURL(pageURL, fmt.Sprintf("/comic/%s/%s-chapter-%s-%s", slug, hid, chap, lang)),
			Title:      title,
			NumMain:    main,
			SuffixType: suffixType,
			SuffixNum:  suffixNum,
			Label:      label,
		})
	}
	return out
}

func parseChapter(raw string) (int, string, int, string, bool) {
	raw = strings.TrimSpace(raw)
	separator := ""
	if strings.Contains(raw, ".") {
		separator = "."
	} else if strings.Contains(raw, "-") {
		separator = "-"
	}
	if separator == "" {
		main, err := strconv.Atoi(normalizeNumberPart(raw))
		return main, "", 0, strconv.Itoa(main), err == nil
	}
	parts := strings.SplitN(raw, separator, 2)
	main, errMain := strconv.Atoi(normalizeNumberPart(parts[0]))
	// Keep the suffix text verbatim in the label: stripping zeros would
	// collapse "10.05" into "10.5". The int form is only a sort key.
	suffixText := strings.TrimSpace(parts[1])
	suffix, errSuffix := strconv.Atoi(normalizeNumberPart(suffixText))
	if errMain != nil || errSuffix != nil {
		return 0, "", 0, "", false
	}
	return main, separator, suffix, fmt.Sprintf("%d%s%s", main, separator, suffixText), true
}

func normalizeNumberPart(value string) string {
	value = strings.TrimLeft(strings.TrimSpace(value), "0")
	if value == "" {
		return "0"
	}
	return value
}

func dedupeAndSort(chapters []providers.Chapter) []providers.Chapter {
	out := make([]providers.Chapter, 0, len(chapters))
	seen := map[string]bool{}
	for _, ch := range chapters {
		if seen[ch.Label] {
			continue
		}
		seen[ch.Label] = true
		out = append(out, ch)
	}
	providers.SortChapters(out)
	return out
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
