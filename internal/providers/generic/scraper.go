package generic

import (
	"context"
	"encoding/json"
	"errors"
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
	log     ui.Log
	allowed *regexp.Regexp
	checkJS bool
	browser BrowserFetcher
}

type BrowserFetcher interface {
	Fetch(ctx context.Context, target string) (string, error)
}

type BrowserCacheLoader interface {
	LoadCached(ctx context.Context, target string)
}

func NewScraper(c *http.Client, log ui.Log, allowExt []string, checkJS bool, browser BrowserFetcher) *Scraper {
	return &Scraper{
		client:  c,
		log:     log,
		allowed: buildExtRegex(normalizeExtList(allowExt)),
		checkJS: checkJS,
		browser: browser,
	}
}

var (
	chapRe          = regexp.MustCompile(`(?i)(?:vol(?:ume)?[_\-\s]*\d+[_\-\s]*)?(?:chapter|ch|episode|ep)[._\-\s]*0*([0-9]+)(?:[_\-\s]*([.\-])[_\-\s]*([0-9]+))?`)
	chapterDash     = regexp.MustCompile(`(?:chapter|episode)[._\-]?0*([0-9]+)[_\-]?([0-9]+)?`)
	reLikelyChapter = regexp.MustCompile(`(?i)(?:^|[-_/])(?:c|ch|chapter)[-_]?\d+`)

	batoSimple  = regexp.MustCompile(`(?:^|[/\-_])c[h]?[_\-]?(\d+(?:\.\d+)?)`)
	batoVol     = regexp.MustCompile(`vol[_\-]?(\d+)[/_\-]ch[_\-]?(\d+(?:\.\d+)?)`)
	batoPlain   = regexp.MustCompile(`[/\-](\d+(?:\.\d+)?)(?:$|[/\-_])`)
	titlePrefix = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*[.\- ]`)

	reNuxt = regexp.MustCompile(`(?s)window\.__NUXT__\s*=\s*(\{.*?\});`)

	reAnyDigits = regexp.MustCompile(`[0-9]+`)
	reBaseLike  = regexp.MustCompile(`(?:/|^)c?0*([0-9]+)(?:/|$)`)
)

// fetchDOMBody returns the parsed page, its HTML, and whether the browser
// solver produced it (so callers can skip a redundant re-solve).
func (s *Scraper) fetchDOMBody(ctx context.Context, target string) (*goquery.Document, string, bool, error) {
	s.log.Debugf("Fetching URL: %s\n", target)

	body, fromBrowser, err := s.fetchBody(ctx, target)
	if err != nil {
		return nil, "", false, err
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	return doc, body, fromBrowser, err
}

func (s *Scraper) fetchHTMXDOMBody(ctx context.Context, target, referer string) (*goquery.Document, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Referer", referer)
	resp, err := util.DoWithRetry(s.client, req, 3, 500*time.Millisecond)
	if err != nil {
		return nil, "", err
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			s.log.Debugf("Warning: failed to close response body: %v\n", cerr)
		}
	}()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	body := string(data)
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if isCloudflareBlock(body) {
		return nil, "", fmt.Errorf("cloudflare challenge blocked")
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	return doc, body, err
}

// fetchBody returns the page HTML and whether it came from the browser solver.
func (s *Scraper) fetchBody(ctx context.Context, target string) (string, bool, error) {
	s.log.Debugf("Fetching body for URL: %s\n", target)
	if loader, ok := s.browser.(BrowserCacheLoader); ok {
		loader.LoadCached(ctx, target)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return "", false, err
	}
	s.log.Debugf("HTTP Request: %s %s\n", req.Method, req.URL.String())

	resp, err := util.DoWithRetry(s.client, req, 3, 500*time.Millisecond)
	if err != nil {
		// Definitive 4xx answers (dead URL etc.) won't change in a browser;
		// transport errors and 5xx may be a challenge, so try the solver.
		var statusErr *util.StatusError
		definitive := errors.As(err, &statusErr) && statusErr.Code < http.StatusInternalServerError
		if definitive || s.browser == nil {
			return "", false, err
		}
		s.log.Infof("Normal HTTP fetch failed for %s: %v\n", target, err)
		html, berr := s.fetchViaBrowser(ctx, target)
		return html, true, berr
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			s.log.Debugf("Warning: failed to close response body: %v\n", cerr)
		}
	}()

	s.log.Debugf("HTTP Response Status: %s\n", resp.Status)

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, err
	}
	body := string(data)

	if resp.StatusCode == http.StatusForbidden || isCloudflareBlock(body) {
		s.log.Infof("Cloudflare protection detected for %s.\n", target)
		if s.browser != nil {
			html, berr := s.fetchViaBrowser(ctx, target)
			return html, true, berr
		}
		s.log.Infof("Browser solver is disabled. Enable FlareSolverr with browser_solver.enabled: true and browser_solver.endpoint: http://localhost:8191/v1.\n")
		return "", false, fmt.Errorf("cloudflare challenge blocked; enable FlareSolverr with browser_solver.enabled: true")
	}

	return body, false, nil
}

func isCloudflareBlock(body string) bool {
	body = strings.ToLower(body)
	return strings.Contains(body, "cloudflare") &&
		(strings.Contains(body, "just a moment") ||
			strings.Contains(body, "attention required") ||
			strings.Contains(body, "sorry, you have been blocked"))
}

func (s *Scraper) fetchViaBrowser(ctx context.Context, target string) (string, error) {
	s.log.Infof("Using browser fetcher for %s.\n", target)
	html, err := s.browser.Fetch(ctx, target)
	if err != nil {
		return "", fmt.Errorf("fetch via browser: %w", err)
	}
	s.log.Infof("Browser fetcher returned HTML for %s (%d bytes).\n", target, len(html))
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

	return reAnyDigits.MatchString(relPath)
}

func parseFromBaseLike(href string) (int, string, bool) {
	if m := reBaseLike.FindStringSubmatch(href); m != nil {
		n, _ := strconv.Atoi(m[1])

		return n, fmt.Sprintf("%d", n), true
	}

	return 0, "", false
}

// chapterLabel is a parsed chapter numbering: the main number, an optional
// sub-number with its separator, and the display label.
type chapterLabel struct {
	Num        int
	SuffixType string
	SuffixNum  int
	Label      string
}

func parseChapterLabel(href, title string) (chapterLabel, bool) {
	h := strings.ToLower(href)
	t := strings.ToLower(strings.TrimSpace(title))

	if isExcluded(h) {
		return chapterLabel{}, false
	}

	if n, label, ok := parseFromBaseLike(h); ok {
		return chapterLabel{Num: n, Label: label}, true
	}

	if !hasChapterKeywords(h, t) {
		return chapterLabel{}, false
	}

	for _, match := range []func() (chapterLabel, bool){
		func() (chapterLabel, bool) { return matchChapterDash(h) },
		func() (chapterLabel, bool) { return matchBatoVol(h) },
		func() (chapterLabel, bool) { return matchBatoSimple(h) },
		func() (chapterLabel, bool) { return matchBatoPlain(h) },
		func() (chapterLabel, bool) { return matchTitlePrefix(title) },
		func() (chapterLabel, bool) { return matchChapRe(title) },
	} {
		if parsed, ok := match(); ok {
			return parsed, true
		}
	}

	return chapterLabel{}, false
}

func hasChapterKeywords(h, t string) bool {
	return strings.Contains(h, "ch") ||
		strings.Contains(h, "chapter") ||
		strings.Contains(h, "episode") ||
		strings.Contains(h, "vol") ||
		strings.Contains(t, "ch") ||
		strings.Contains(t, "chapter") ||
		strings.Contains(t, "episode") ||
		strings.Contains(t, "vol")
}

func isExcluded(h string) bool {
	for _, token := range []string{"/u/", "batolists", "/page/", "/pg/", "?page=", "&page="} {
		if strings.Contains(h, token) {
			return true
		}
	}
	return false
}

func matchChapterDash(h string) (chapterLabel, bool) {
	if m := chapterDash.FindStringSubmatch(h); m != nil {
		main, _ := strconv.Atoi(m[1])
		if m[2] != "" {
			sub, _ := strconv.Atoi(m[2])
			return chapterLabel{Num: main, SuffixType: "-", SuffixNum: sub, Label: fmt.Sprintf("%d-%d", main, sub)}, true
		}
		return chapterLabel{Num: main, Label: strconv.Itoa(main)}, true
	}

	return chapterLabel{}, false
}

// matchBatoVol reads vol_X/ch_Y URLs; the volume is dropped because chapter
// numbering on these sites continues across volumes.
func matchBatoVol(h string) (chapterLabel, bool) {
	if m := batoVol.FindStringSubmatch(h); m != nil {
		return parseDecimalLabel(m[2])
	}

	return chapterLabel{}, false
}

func matchBatoSimple(h string) (chapterLabel, bool) {
	if m := batoSimple.FindStringSubmatch(h); m != nil {
		return parseDecimalLabel(m[1])
	}

	return chapterLabel{}, false
}

// parseDecimalLabel parses "12" or "12.5" style chapter numbers.
func parseDecimalLabel(raw string) (chapterLabel, bool) {
	parts := strings.Split(raw, ".")
	main, _ := strconv.Atoi(parts[0])
	if len(parts) == 2 {
		sub, _ := strconv.Atoi(parts[1])
		return chapterLabel{Num: main, SuffixType: ".", SuffixNum: sub, Label: fmt.Sprintf("%d.%s", main, parts[1])}, true
	}
	return chapterLabel{Num: main, Label: strconv.Itoa(main)}, true
}

func matchBatoPlain(h string) (chapterLabel, bool) {
	if m := batoPlain.FindStringSubmatch(h); m != nil {
		n, _ := strconv.Atoi(m[1])
		return chapterLabel{Num: n, Label: m[1]}, true
	}

	return chapterLabel{}, false
}

func matchTitlePrefix(title string) (chapterLabel, bool) {
	if m := titlePrefix.FindStringSubmatch(title); m != nil {
		n, _ := strconv.Atoi(m[1])
		return chapterLabel{Num: n, Label: m[1]}, true
	}

	return chapterLabel{}, false
}

func matchChapRe(title string) (chapterLabel, bool) {
	if m := chapRe.FindStringSubmatch(title); m != nil {
		main, _ := strconv.Atoi(m[1])
		typ := m[2]
		sub, _ := strconv.Atoi(m[3])
		if typ == "" {
			return chapterLabel{Num: main, Label: strconv.Itoa(main)}, true
		}
		return chapterLabel{Num: main, SuffixType: typ, SuffixNum: sub, Label: fmt.Sprintf("%d%s%d", main, typ, sub)}, true
	}

	return chapterLabel{}, false
}

func looksLikeChapterLink(href, title string) bool {
	h := strings.ToLower(href)
	if reLikelyChapter.MatchString(h) || batoVol.MatchString(h) || batoSimple.MatchString(h) {
		return true
	}

	t := strings.ToLower(strings.TrimSpace(title))

	return strings.HasPrefix(t, "ch ") ||
		strings.HasPrefix(t, "chapter ") ||
		strings.HasPrefix(t, "ep ") ||
		strings.HasPrefix(t, "episode ")
}

func (s *Scraper) GetChapters(ctx context.Context, pageURL string) ([]providers.Chapter, error) {
	doc, body, fromBrowser, err := s.fetchDOMBody(ctx, pageURL)
	if err != nil {
		s.log.Debugf("Failed to fetch DOM: %v\n", err)
		return nil, err
	}

	out := scanChapterLinks(doc, pageURL, s.log)
	out = s.expandChapterListIfGapped(ctx, pageURL, doc, out)
	if len(out) == 0 && s.browser != nil && !fromBrowser {
		s.log.Infof("No static chapter links found for %s; trying browser-rendered HTML.\n", pageURL)
		body, err = s.fetchViaBrowser(ctx, pageURL)
		if err != nil {
			return nil, err
		}
		doc, err = goquery.NewDocumentFromReader(strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		out = scanChapterLinks(doc, pageURL, s.log)
		out = s.expandChapterListIfGapped(ctx, pageURL, doc, out)
	}
	if len(out) == 0 && looksDynamicApp(body) {
		return nil, fmt.Errorf("no static chapter links found; enable browser_solver.enabled (FlareSolverr) for JS-rendered chapter lists")
	}

	return out, nil
}

// ScanChapterLinks extracts chapter links from a parsed page, for scrapers
// that fetch chapter-list HTML from source-specific endpoints.
func ScanChapterLinks(doc *goquery.Document, pageURL string, log ui.Log) []providers.Chapter {
	return scanChapterLinks(doc, pageURL, log)
}

func (s *Scraper) expandChapterListIfGapped(ctx context.Context, pageURL string, doc *goquery.Document, chapters []providers.Chapter) []providers.Chapter {
	if !hasDefinitiveChapterGap(chapters) {
		return chapters
	}
	for _, u := range chapterExpansionURLs(doc, pageURL) {
		s.log.Infof("Chapter gap detected; trying expanded chapter list %s.\n", u)
		nextDoc, _, _, err := s.fetchDOMBody(ctx, u)
		if err != nil {
			s.log.Debugf("Skipping chapter expansion %s: %v\n", u, err)
			continue
		}
		if expanded := mergeChapters(chapters, scanChapterLinks(nextDoc, pageURL, s.log)); len(expanded) > len(chapters) {
			return expanded
		}
	}
	return chapters
}

func scanChapterLinks(doc *goquery.Document, pageURL string, log ui.Log) []providers.Chapter {
	var out []providers.Chapter
	seen := map[string]bool{}
	seenLabel := map[string]bool{}

	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		log.Debugf("Found link: %s (text: %s)\n", href, strings.TrimSpace(a.Text()))

		if !looksLikeChapterLink(href, a.Text()) && !isLikelyChapterFromBase(pageURL, href) {
			return
		}

		parsed, ok := parseChapterLabel(strings.TrimSpace(href), strings.TrimSpace(a.Text()))
		if !ok {
			return
		}

		if !sameSeriesChapterLink(pageURL, href) {
			return
		}

		log.Debugf("Link looks like chapter link: %s\n", href)
		if seenLabel[parsed.Label] {
			return
		}

		u := providers.ResolveURL(pageURL, href)
		if seen[u] {
			return
		}
		seen[u] = true
		seenLabel[parsed.Label] = true

		title := strings.TrimSpace(a.Text())
		if before, _, ok := strings.Cut(title, "\n"); ok {
			title = strings.TrimSpace(before)
		}
		if title == "" {
			title = "Chapter " + parsed.Label
		}

		out = append(out, providers.Chapter{
			URL:        u,
			Title:      title,
			NumMain:    parsed.Num,
			SuffixType: parsed.SuffixType,
			SuffixNum:  parsed.SuffixNum,
			Label:      parsed.Label,
		})
	})

	providers.SortChapters(out)

	return out
}

func hasDefinitiveChapterGap(chapters []providers.Chapter) bool {
	if len(chapters) < 3 {
		return false
	}
	min, max := chapters[0].NumMain, chapters[0].NumMain
	seen := map[int]bool{}
	for _, ch := range chapters {
		if ch.SuffixType != "" {
			continue
		}
		seen[ch.NumMain] = true
		if ch.NumMain < min {
			min = ch.NumMain
		}
		if ch.NumMain > max {
			max = ch.NumMain
		}
	}
	if len(seen) < 3 {
		return false
	}
	return max-min+1-len(seen) >= 10
}

func chapterExpansionURLs(doc *goquery.Document, pageURL string) []string {
	var out []string
	seen := map[string]bool{}
	doc.Find("[hx-get],a[href]").Each(func(_ int, s *goquery.Selection) {
		raw, _ := s.Attr("hx-get")
		if raw == "" {
			raw, _ = s.Attr("href")
		}
		signal := strings.ToLower(raw + " " + attr(s, "hx-target") + " " + s.Text())
		if !strings.Contains(signal, "chapter") ||
			!(strings.Contains(signal, "full") || strings.Contains(signal, "show all") || strings.Contains(signal, "load more") || strings.Contains(signal, "chapter-list")) {
			return
		}
		u := providers.ResolveURL(pageURL, raw)
		if !sameHost(pageURL, u) {
			return
		}
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	})
	return out
}

func sameHost(a, b string) bool {
	au, err := url.Parse(a)
	if err != nil {
		return false
	}
	bu, err := url.Parse(b)
	return err == nil && au.Host == bu.Host
}

func attr(s *goquery.Selection, name string) string {
	v, _ := s.Attr(name)
	return v
}

func mergeChapters(a, b []providers.Chapter) []providers.Chapter {
	out := append([]providers.Chapter{}, a...)
	seen := map[string]bool{}
	for _, ch := range out {
		seen[ch.Label] = true
	}
	for _, ch := range b {
		if !seen[ch.Label] {
			out = append(out, ch)
		}
	}
	providers.SortChapters(out)
	return out
}

func looksDynamicApp(body string) bool {
	return strings.Contains(body, `id="app-root"`) || strings.Contains(body, `id='app-root'`) ||
		strings.Contains(body, `id="initial-data"`) || strings.Contains(body, `id='initial-data'`)
}

func sameSeriesChapterLink(pageURL, href string) bool {
	base, err := url.Parse(pageURL)
	if err != nil {
		return false
	}
	candidate, err := url.Parse(providers.ResolveURL(pageURL, href))
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
	if sameSeriesID(baseParts, candidateParts) {
		return true
	}
	if sameSeriesSlug(baseParts, candidateParts) {
		return true
	}
	if candidateParts[0] == "chapters" && !hasNumericPathPart(baseParts) {
		return true
	}
	if baseParts[0] != candidateParts[0] {
		return false
	}
	return baseParts[1] == candidateParts[1]
}

func hasNumericPathPart(parts []string) bool {
	for _, part := range parts {
		if _, err := strconv.Atoi(part); err == nil {
			return true
		}
	}
	return false
}

// sameSeriesSlug accepts chapter URLs whose own slug embeds the series slug,
func sameSeriesSlug(baseParts, candidateParts []string) bool {
	slug := baseParts[len(baseParts)-1]
	if len(slug) < 4 {
		return false // too short to be a distinctive series slug
	}
	for _, part := range candidateParts {
		if strings.HasPrefix(part, slug+"-") {
			return true
		}
	}
	return false
}

func sameSeriesID(baseParts, candidateParts []string) bool {
	for _, basePart := range baseParts {
		if _, err := strconv.Atoi(basePart); err != nil {
			continue
		}
		for _, candidatePart := range candidateParts {
			if candidatePart == basePart || strings.HasPrefix(candidatePart, basePart+"-") {
				return true
			}
		}
	}
	return false
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
	doc, body, usedBrowser, err := s.fetchDOMBody(ctx, chapterURL)
	if err != nil {
		s.log.Debugf("Failed to fetch DOM: %v\n", err)
		return nil, err
	}
	if looksDynamicApp(body) && s.browser != nil && !usedBrowser {
		s.log.Infof("JS-rendered chapter page detected for %s; trying browser-rendered HTML.\n", chapterURL)
		body, err = s.fetchViaBrowser(ctx, chapterURL)
		if err != nil {
			return nil, err
		}
		doc, err = goquery.NewDocumentFromReader(strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		usedBrowser = true
	}

	s.log.Debugf("Fetched DOM for URL: %s\n", chapterURL)

	// s.log.Debugf("\n======= DEBUG HTML START =======\n%s\n======= DEBUG HTML END =======\n\n", body)

	col := newImageCollector(s.allowed, s.log)
	visited := map[string]bool{chapterURL: true}

	s.scanImages(ctx, col, doc, body, chapterURL)
	for _, pageURL := range chapterPageURLs(doc, chapterURL) {
		if visited[pageURL] {
			continue
		}
		visited[pageURL] = true
		nextDoc, nextBody, _, err := s.fetchDOMBody(ctx, pageURL)
		if err != nil {
			s.log.Debugf("Skipping chapter page %s: %v\n", pageURL, err)
			continue
		}
		s.scanImages(ctx, col, nextDoc, nextBody, pageURL)
	}
	s.scanImageFragments(ctx, col, doc, chapterURL)

	final := col.Finalize()
	if len(final) == 0 && s.browser != nil && !usedBrowser {
		s.log.Infof("No static reader images found for %s; trying browser-rendered HTML.\n", chapterURL)
		body, err = s.fetchViaBrowser(ctx, chapterURL)
		if err != nil {
			return nil, err
		}
		doc, err = goquery.NewDocumentFromReader(strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		col = newImageCollector(s.allowed, s.log)
		s.scanImages(ctx, col, doc, body, chapterURL)
		final = col.Finalize()
	}
	if len(final) == 0 {
		return nil, fmt.Errorf("no usable images found")
	}

	return final, nil
}

func (s *Scraper) scanImageFragments(ctx context.Context, col *imageCollector, doc *goquery.Document, chapterURL string) {
	for _, u := range imageFragmentURLs(doc, chapterURL) {
		s.log.Infof("Trying reader image fragment %s.\n", u)
		nextDoc, nextBody, err := s.fetchHTMXDOMBody(ctx, u, chapterURL)
		if err != nil {
			s.log.Debugf("Skipping reader image fragment %s: %v\n", u, err)
			continue
		}
		s.scanImages(ctx, col, nextDoc, nextBody, u)
	}
}

func imageFragmentURLs(doc *goquery.Document, chapterURL string) []string {
	var out []string
	seen := map[string]bool{}
	doc.Find("[hx-get]").Each(func(_ int, s *goquery.Selection) {
		raw, _ := s.Attr("hx-get")
		signal := strings.ToLower(raw + " " + attr(s, "hx-target") + " " + attr(s, "hx-include") + " " + s.Text())
		if !strings.Contains(signal, "image") {
			return
		}
		u := withDefaultQuery(providers.ResolveURL(chapterURL, raw), "reading_style", "long_strip")
		if !sameHost(chapterURL, u) || seen[u] {
			return
		}
		seen[u] = true
		out = append(out, u)
	})
	return out
}

func withDefaultQuery(raw, key, value string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	if q.Get(key) == "" {
		q.Set(key, value)
		u.RawQuery = q.Encode()
	}
	return u.String()
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
		u := providers.ResolveURL(chapterURL, strings.TrimSpace(raw))
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
