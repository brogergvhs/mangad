package generic

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
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

//go:embed selenium_fetch.py
var embeddedSeleniumScript []byte

type Scraper struct {
	client  *http.Client
	log     *ui.Logger
	allowed *regexp.Regexp
	checkJS bool
	withCF  bool
}

func NewScraper(c *http.Client, log *ui.Logger, allowExt []string, checkJS bool, withCF bool) *Scraper {
	return &Scraper{
		client:  c,
		log:     log,
		allowed: buildExtRegex(normalizeExtList(allowExt)),
		checkJS: checkJS,
		withCF:  withCF,
	}
}

var (
	chapRe      = regexp.MustCompile(`(?i)(?:vol(?:ume)?[_\-\s]*\d+[_\-\s]*)?(?:chapter|ch)[_\-\s]*0*([0-9]+)(?:[_\-\s]*([.\-])[_\-\s]*([0-9]+))?`)
	chapterDash = regexp.MustCompile(`chapter[_\-]?0*([0-9]+)[_\-]?([0-9]+)?`)

	batoSimple  = regexp.MustCompile(`(?:^|[/\-_])ch[_\-]?(\d+(?:\.\d+)?)`)
	batoVol     = regexp.MustCompile(`vol[_\-]?(\d+)[/_\-]ch[_\-]?(\d+(?:\.\d+)?)`)
	batoPlain   = regexp.MustCompile(`[/\-](\d+(?:\.\d+)?)(?:$|[/\-_])`)
	titlePrefix = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*[.\- ]`)

	reLikelyChapter = regexp.MustCompile(`(?i)(?:^|[-_/])(?:ch|chapter)[-_]?\d+`)
	reNuxt          = regexp.MustCompile(`window\.__NUXT__\s*=\s*(\{.*?});`)
)

func (s *Scraper) fetchDOM(ctx context.Context, target string) (*goquery.Document, error) {
	s.log.Debugf("Fetching URL: %s\n", target)

	body, err := s.fetchBody(ctx, target)
	if err != nil {
		return nil, err
	}

	return goquery.NewDocumentFromReader(strings.NewReader(body))
}

func (s *Scraper) fetchBody(ctx context.Context, target string) (string, error) {
	s.log.Debugf("Fetching body for URL: %s\n", target)

	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return "", err
	}
	s.log.Debugf("HTTP Request: %s %s\n", req.Method, req.URL.String())

	resp, err := util.DoWithRetry(s.client, req, 3, 500*time.Millisecond)
	if err != nil {
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
		if !s.withCF {
			s.log.Infof("Cloudflare protection detected for %s.\n", target)
			s.log.Infof("Selenium fallback disabled. Re-run with --with-cf or enable with_cf in config to allow bypass.\n")
			return "", fmt.Errorf("cloudflare challenge blocked (use --with-cf to allow bypass)")
		}

		tmpFile, err := os.CreateTemp("", "selenium_fetch_*.py")
		if err != nil {
			return "", fmt.Errorf("failed to create temp script: %w", err)
		}
		defer func() {
			if err := os.Remove(tmpFile.Name()); err != nil {
				s.log.Debugf("Warning: failed to remove temp file %s: %v\n", tmpFile.Name(), err)
			}
		}()

		if _, err := tmpFile.Write(embeddedSeleniumScript); err != nil {
			if cerr := tmpFile.Close(); cerr != nil {
				s.log.Debugf("Warning: failed to close temp file: %v\n", cerr)
			}
			return "", fmt.Errorf("failed to write embedded selenium script: %w", err)
		}
		if err := tmpFile.Close(); err != nil {
			s.log.Debugf("Warning: failed to close temp file: %v\n", err)
		}

		s.log.Debugf("Running embedded Selenium script for %s\n", target)
		cmd := exec.CommandContext(ctx, "python3", tmpFile.Name(), target)

		var out bytes.Buffer
		var stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr

		err = cmd.Run()
		if err != nil {
			s.log.Errorf("Selenium script failed: %v\nstderr: %s\n", err, stderr.String())
			return "", err
		}

		html := out.String()
		s.log.Debugf("Fetched body via embedded Selenium for %s\n", target)

		return html, nil
	}

	return body, nil
}

func parseChapterLabel(href, title string) (int, string, int, string, bool) {
	h := strings.ToLower(href)
	t := strings.ToLower(title)

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
		return nil, err
	}

	var out []providers.Chapter
	seen := map[string]bool{}

	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		if !looksLikeChapterLink(href, a.Text()) {
			return
		}

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

func (s *Scraper) GetImages(ctx context.Context, chapterURL string) ([]string, error) {
	doc, err := s.fetchDOM(ctx, chapterURL)
	if err != nil {
		return nil, err
	}

	body, _ := s.fetchBody(ctx, chapterURL)

	// s.log.Debugf("\n======= DEBUG HTML START =======\n%s\n======= DEBUG HTML END =======\n\n", body)

	col := newImageCollector(s.allowed, s.log.Debug)

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

	final := col.Finalize()
	if len(final) == 0 {
		return nil, fmt.Errorf("no usable images found")
	}

	return final, nil
}
