package generic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/brogergvhs/mangad/internal/providers"
	"github.com/brogergvhs/mangad/internal/ui"
	"github.com/brogergvhs/mangad/internal/util"
)

type Scraper struct {
	client  *http.Client
	log     *ui.Logger
	allowed *regexp.Regexp
	checkJS bool
	browser BrowserFetcher
}

type BrowserFetcher interface {
	LoadCached(ctx context.Context, target string)
	Fetch(ctx context.Context, target string) (string, error)
}

func NewScraper(c *http.Client, log *ui.Logger, allowExt []string, checkJS bool, browser BrowserFetcher) *Scraper {
	return &Scraper{
		client:  c,
		log:     log,
		allowed: buildExtRegex(normalizeExtList(allowExt)),
		checkJS: checkJS,
		browser: browser,
	}
}

var (
	chapRe          = regexp.MustCompile(`(?i)(?:vol(?:ume)?[_\-\s]*\d+[_\-\s]*)?(?:chapter|ch)[_\-\s]*0*([0-9]+)(?:[_\-\s]*([.\-])[_\-\s]*([0-9]+))?`)
	chapterDash     = regexp.MustCompile(`chapter[_\-]?0*([0-9]+)[_\-]?([0-9]+)?`)
	reLikelyChapter = regexp.MustCompile(`(?i)(?:^|[-_/])(?:c|ch|chapter)[-_]?\d+`)

	batoSimple  = regexp.MustCompile(`(?:^|[/\-_])c[h]?[_\-]?(\d+(?:\.\d+)?)`)
	batoVol     = regexp.MustCompile(`vol[_\-]?(\d+)[/_\-]ch[_\-]?(\d+(?:\.\d+)?)`)
	batoPlain   = regexp.MustCompile(`[/\-](\d+(?:\.\d+)?)(?:$|[/\-_])`)
	titlePrefix = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*[.\- ]`)

	reNuxt = regexp.MustCompile(`window\.__NUXT__\s*=\s*(\{.*?});`)
)

func (s *Scraper) fetchDOM(ctx context.Context, target string) (*goquery.Document, error) {
	doc, _, err := s.fetchDOMBody(ctx, target)
	if err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *Scraper) fetchDOMBody(ctx context.Context, target string) (*goquery.Document, string, error) {
	s.log.Debugf("Fetching URL: %s\n", target)

	body, err := s.fetchBody(ctx, target)
	if err != nil {
		return nil, "", err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	return doc, body, err
}

func (s *Scraper) fetchBody(ctx context.Context, target string) (string, error) {
	s.log.Debugf("Fetching body for URL: %s\n", target)
	if s.browser != nil {
		s.browser.LoadCached(ctx, target)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return "", err
	}
	s.log.Debugf("HTTP Request: %s %s\n", req.Method, req.URL.String())

	resp, err := util.DoWithRetry(s.client, req, 3, 500*time.Millisecond)
	if err != nil {
		if s.browser != nil {
			s.log.Infof("Normal HTTP fetch failed for %s: %v\n", target, err)
			return s.fetchViaBrowser(ctx, target)
		}
		return "", err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			s.log.Debugf("Warning: failed to close response body: %v\n", cerr)
		}
	}()

	s.log.Debugf("HTTP Response Status: %s\n", resp.Status)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	body := string(data)

	if resp.StatusCode == http.StatusForbidden || strings.Contains(body, "Just a moment") {
		s.log.Infof("Cloudflare protection detected for %s.\n", target)
		if s.browser != nil {
			return s.fetchViaBrowser(ctx, target)
		}
		s.log.Infof("Browser solver is disabled. Enable FlareSolverr with browser_solver.enabled: true and browser_solver.endpoint: http://localhost:8191/v1.\n")
		return "", fmt.Errorf("cloudflare challenge blocked; enable FlareSolverr with browser_solver.enabled: true")
	}

	return body, nil
}

func (s *Scraper) fetchViaBrowser(ctx context.Context, target string) (string, error) {
	s.log.Infof("Using browser solver for %s.\n", target)
	html, err := s.browser.Fetch(ctx, target)
	if err != nil {
		return "", fmt.Errorf("fetch via browser solver: %w", err)
	}
	s.log.Infof("Browser solver returned HTML for %s (%d bytes).\n", target, len(html))
	return html, nil
}

// isLikelyChapterFromBase returns true if href looks like a chapter link derived from the same series URL.
func isLikelyChapterFromBase(baseURL, href string) bool {
	base, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	h, err := url.Parse(href)
	if err != nil {
		return false
	}

	full := base.ResolveReference(h)

	if full.Host != base.Host {
		return false
	}
	if !strings.HasPrefix(full.Path, base.Path) {
		return false
	}

	relPath := strings.TrimPrefix(full.Path, base.Path)
	relPath = strings.Trim(relPath, "/")

	if relPath == "" {
		return false
	}

	return regexp.MustCompile(`[0-9]+`).MatchString(relPath)
}

func parseFromBaseLike(href string) (int, string, bool) {
	re := regexp.MustCompile(`(?:/|^)c?0*([0-9]+)(?:/|$)`)
	if m := re.FindStringSubmatch(href); m != nil {
		n, _ := strconv.Atoi(m[1])

		return n, fmt.Sprintf("%d", n), true
	}

	return 0, "", false
}

func parseChapterLabel(href, title string) (int, string, int, string, bool) {
	h := strings.ToLower(href)
	t := strings.ToLower(title)

	if n, label, ok := parseFromBaseLike(h); ok {
		return n, "", 0, label, true
	}

	if !hasChapterKeywords(h, t) || isExcluded(h) {
		return 0, "", 0, "", false
	}

	if n, typ, sn, label, ok := matchChapterDash(h); ok {
		return n, typ, sn, label, true
	}
	if n, typ, sn, label, ok := matchBatoVol(h); ok {
		return n, typ, sn, label, true
	}
	if n, typ, sn, label, ok := matchBatoSimple(h); ok {
		return n, typ, sn, label, true
	}
	if n, typ, sn, label, ok := matchBatoPlain(h); ok {
		return n, typ, sn, label, true
	}
	if n, typ, sn, label, ok := matchTitlePrefix(title); ok {
		return n, typ, sn, label, true
	}
	if n, typ, sn, label, ok := matchChapRe(title); ok {
		return n, typ, sn, label, true
	}

	return 0, "", 0, "", false
}

func hasChapterKeywords(h, t string) bool {
	return strings.Contains(h, "ch") ||
		strings.Contains(h, "chapter") ||
		strings.Contains(h, "vol") ||
		strings.Contains(t, "ch") ||
		strings.Contains(t, "chapter") ||
		strings.Contains(t, "vol")
}

func isExcluded(h string) bool {
	return strings.Contains(h, "/u/") || strings.Contains(h, "batolists")
}

func matchChapterDash(h string) (int, string, int, string, bool) {
	if m := chapterDash.FindStringSubmatch(h); m != nil {
		main, _ := strconv.Atoi(m[1])
		if m[2] != "" {
			sub, _ := strconv.Atoi(m[2])

			return main, "-", sub, fmt.Sprintf("%d-%d", main, sub), true
		}

		return main, "", 0, fmt.Sprintf("%d", main), true
	}

	return 0, "", 0, "", false
}

func matchBatoVol(h string) (int, string, int, string, bool) {
	if m := batoVol.FindStringSubmatch(h); m != nil {
		vol, _ := strconv.Atoi(m[1])
		ch, _ := strconv.Atoi(m[2])

		return ch, ".", vol, fmt.Sprintf("%d.%d", vol, ch), true
	}

	return 0, "", 0, "", false
}

func matchBatoSimple(h string) (int, string, int, string, bool) {
	if m := batoSimple.FindStringSubmatch(h); m != nil {
		parts := strings.Split(m[1], ".")
		main, _ := strconv.Atoi(parts[0])
		if len(parts) == 2 {
			sub, _ := strconv.Atoi(parts[1])

			return main, ".", sub, fmt.Sprintf("%d.%d", main, sub), true
		}

		return main, "", 0, fmt.Sprintf("%d", main), true
	}

	return 0, "", 0, "", false
}

func matchBatoPlain(h string) (int, string, int, string, bool) {
	if m := batoPlain.FindStringSubmatch(h); m != nil {
		n, _ := strconv.Atoi(m[1])

		return n, "", 0, m[1], true
	}

	return 0, "", 0, "", false
}

func matchTitlePrefix(title string) (int, string, int, string, bool) {
	if m := titlePrefix.FindStringSubmatch(title); m != nil {
		n, _ := strconv.Atoi(m[1])

		return n, "", 0, m[1], true
	}

	return 0, "", 0, "", false
}

func matchChapRe(title string) (int, string, int, string, bool) {
	if m := chapRe.FindStringSubmatch(title); m != nil {
		main, _ := strconv.Atoi(m[1])
		typ := m[2]
		sub, _ := strconv.Atoi(m[3])
		label := fmt.Sprintf("%d%s%d", main, typ, sub)

		if typ == "" {
			label = fmt.Sprintf("%d", main)
		}

		return main, typ, sub, label, true
	}

	return 0, "", 0, "", false
}

func looksLikeChapterLink(href, title string) bool {
	h := strings.ToLower(href)
	if reLikelyChapter.MatchString(h) || batoVol.MatchString(h) || batoSimple.MatchString(h) {
		return true
	}

	t := strings.ToLower(title)

	return strings.HasPrefix(t, "ch ") || strings.HasPrefix(t, "chapter ")
}

func resolveURL(baseURL, href string) string {
	if href == "" {
		return baseURL
	}

	u, err := url.Parse(href)
	if err == nil && u.IsAbs() {
		return u.String()
	}

	b, err := url.Parse(baseURL)
	if err != nil {
		return href
	}

	return b.ResolveReference(u).String()
}

func (s *Scraper) GetChapters(ctx context.Context, pageURL string) ([]providers.Chapter, error) {
	doc, err := s.fetchDOM(ctx, pageURL)
	if err != nil {
		s.log.Debugf("Failed to fetch DOM: %v\n", err)
		return nil, err
	}

	var out []providers.Chapter
	seen := map[string]bool{}

	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		s.log.Debugf("Found link: %s (text: %s)\n", href, strings.TrimSpace(a.Text()))

		if !looksLikeChapterLink(href, a.Text()) && !isLikelyChapterFromBase(pageURL, href) {
			return
		}

		if !sameSeriesChapterLink(pageURL, href) {
			return
		}

		s.log.Debugf("Link looks like chapter link: %s\n", href)

		n, t, sn, label, ok := parseChapterLabel(strings.TrimSpace(href), strings.TrimSpace(a.Text()))
		if !ok {
			return
		}

		u := resolveURL(pageURL, href)
		if seen[u] {
			return
		}
		seen[u] = true

		title := strings.TrimSpace(a.Text())
		if title == "" {
			title = "Chapter " + label
		}

		out = append(out, providers.Chapter{
			URL:        u,
			Title:      title,
			NumMain:    n,
			SuffixType: t,
			SuffixNum:  sn,
			Label:      label,
		})
	})

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NumMain != out[j].NumMain {
			return out[i].NumMain < out[j].NumMain
		}
		if out[i].SuffixType != out[j].SuffixType {
			return out[i].SuffixType < out[j].SuffixType
		}
		return out[i].SuffixNum < out[j].SuffixNum
	})

	return out, nil
}

func sameSeriesChapterLink(pageURL, href string) bool {
	base, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	candidate, err := url.Parse(resolveURL(pageURL, href))
	if err != nil {
		return false
	}
	if candidate.Host != base.Host {
		return false
	}

	baseParts := pathParts(base.Path)
	candidateParts := pathParts(candidate.Path)
	if len(baseParts) < 2 || len(candidateParts) < 2 {
		return strings.HasPrefix(candidate.Path, chapterPageBase(base.Path))
	}
	if baseParts[0] != candidateParts[0] {
		return false
	}
	return baseParts[1] == candidateParts[1]
}

func pathParts(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	out := parts[:0]
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func (s *Scraper) GetImages(ctx context.Context, chapterURL string) ([]string, error) {
	doc, body, err := s.fetchDOMBody(ctx, chapterURL)
	if err != nil {
		s.log.Debugf("Failed to fetch DOM: %v\n", err)
		return nil, err
	}

	s.log.Debugf("Fetched DOM for URL: %s\n", chapterURL)

	// s.log.Debugf("\n======= DEBUG HTML START =======\n%s\n======= DEBUG HTML END =======\n\n", body)

	col := newImageCollector(s.allowed, s.log.Debug)
	visited := map[string]bool{chapterURL: true}

	s.scanImages(ctx, col, doc, body, chapterURL)
	for _, pageURL := range chapterPageURLs(doc, chapterURL) {
		if visited[pageURL] {
			continue
		}
		visited[pageURL] = true
		nextDoc, nextBody, err := s.fetchDOMBody(ctx, pageURL)
		if err != nil {
			s.log.Debugf("Skipping chapter page %s: %v\n", pageURL, err)
			continue
		}
		s.scanImages(ctx, col, nextDoc, nextBody, pageURL)
	}

	final := col.Finalize()
	if len(final) == 0 {
		return nil, fmt.Errorf("no usable images found")
	}

	return final, nil
}

func (s *Scraper) scanImages(ctx context.Context, col *imageCollector, doc *goquery.Document, body, chapterURL string) {
	added := col.ScanIMGTags(doc, chapterURL)
	s.log.Debugf("IMG tags: +%d candidates\n", added)

	added = col.ScanPictureSources(doc, chapterURL)
	s.log.Debugf("PICTURE sources: +%d candidates\n", added)

	added = col.ScanAnchorImages(doc, chapterURL)
	s.log.Debugf("ANCHOR href: +%d candidates\n", added)

	added = col.ScanBackgroundImages(doc, chapterURL)
	s.log.Debugf("CSS background: +%d candidates\n", added)

	if match := reNuxt.FindStringSubmatch(body); len(match) > 1 {
		var raw map[string]any
		if json.Unmarshal([]byte(match[1]), &raw) == nil {
			s.log.Debugf("Found embedded Nuxt/SSR-style JSON\n")
			col.ScanNuxt(raw, chapterURL)
		}
	}

	col.ScanLooseURLs(body)

	if s.checkJS {
		s.log.Debugf("JS scraping enabled\n")

		var jsCode strings.Builder
		doc.Find("script").Each(func(_ int, sc *goquery.Selection) {
			t := sc.Text()
			if strings.TrimSpace(t) != "" {
				jsCode.WriteString(t)
				jsCode.WriteString("\n")
			}
		})

		jsAnalysis := ExtractJS(jsCode.String())
		s.log.Debugf("JS Vars: %+v\n", jsAnalysis.Vars)
		s.log.Debugf("JS URLs: %+v\n", jsAnalysis.URLs)
		s.log.Debugf("JS Calls: %+v\n", jsAnalysis.Calls)

		s.probeDynamicEndpoints(ctx, chapterURL, jsAnalysis, col)
	} else {
		s.log.Debugf("JS scraping disabled (use --check-js to enable)\n")
	}
}

func chapterPageURLs(doc *goquery.Document, chapterURL string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(raw string) {
		u := resolveURL(chapterURL, strings.TrimSpace(raw))
		n, ok := chapterPageNumber(chapterURL, u)
		if u == "" || seen[u] || !ok || n == 1 {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	doc.Find("option[value]").Each(func(_ int, opt *goquery.Selection) {
		if value, ok := opt.Attr("value"); ok {
			add(value)
		}
	})
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		if href, ok := a.Attr("href"); ok {
			add(href)
		}
	})
	sort.SliceStable(out, func(i, j int) bool {
		ai, _ := chapterPageNumber(chapterURL, out[i])
		aj, _ := chapterPageNumber(chapterURL, out[j])
		return ai < aj
	})
	return out
}

func chapterPageNumber(chapterURL, candidate string) (int, bool) {
	base, err := url.Parse(chapterURL)
	if err != nil {
		return 0, false
	}
	cand, err := url.Parse(candidate)
	if err != nil {
		return 0, false
	}
	if cand.Host != base.Host {
		return 0, false
	}
	basePath := chapterPageBase(base.Path)
	candPath := cand.EscapedPath()
	if candPath == basePath {
		return 1, true
	}
	if !strings.HasPrefix(candPath, basePath) {
		return 0, false
	}
	leaf := strings.TrimPrefix(candPath, basePath)
	if !strings.HasSuffix(leaf, ".html") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(leaf, ".html"))
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

func chapterPageBase(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasSuffix(p, ".html") {
		if i := strings.LastIndex(p, "/"); i >= 0 {
			p = p[:i+1]
		}
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}
