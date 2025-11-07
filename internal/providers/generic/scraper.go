// Package generic implements a provider.Scraper that works on general
// HTML-based manga reading sites. It extracts chapters and image URLs
// using DOM-first analysis with fallback heuristics.
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

	"github.com/brogergvhs/mangad/internal/providers"
	"github.com/brogergvhs/mangad/internal/util"

	"github.com/PuerkitoBio/goquery"
)

type Scraper struct {
	client  *http.Client
	debug   bool
	allowed *regexp.Regexp
}

func NewScraper(c *http.Client, debug bool, allowExt []string) *Scraper {
	normalized := normalizeExtList(allowExt)

	return &Scraper{
		client:  c,
		debug:   debug,
		allowed: buildExtRegex(normalized),
	}
}

var (
	chapRe      = regexp.MustCompile(`(?i)(?:vol(?:ume)?[_\-\s]*\d+[_\-\s]*)?(?:chapter|ch)[_\-\s]*0*([0-9]+)(?:[_\-\s]*([.\-])[_\-\s]*([0-9]+))?`)
	chapterDash = regexp.MustCompile(`chapter[_\-]?0*([0-9]+)[_\-]?([0-9]+)?`)

	reLikelyChapter = regexp.MustCompile(`(?i)(?:^|[-_/])(?:ch|chapter)[-_]?\d+`)
	disallowedExt   = regexp.MustCompile(`(?i)\.(?:gif)$`)
)

var (
	batoSimple  = regexp.MustCompile(`(?:^|[/\-_])ch[_\-]?(\d+(?:\.\d+)?)`)
	batoVol     = regexp.MustCompile(`vol[_\-]?(\d+)[/_\-]ch[_\-]?(\d+(?:\.\d+)?)`)
	batoPlain   = regexp.MustCompile(`[/\-](\d+(?:\.\d+)?)(?:$|[/\-_])`)
	titlePrefix = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*[.\- ]`)
)

func (s *Scraper) fetchDOM(ctx context.Context, target string) (*goquery.Document, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, err
	}

	resp, err := util.DoWithRetry(s.client, req, 3, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	return goquery.NewDocumentFromReader(resp.Body)
}

func (s *Scraper) fetchBody(ctx context.Context, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return "", err
	}

	resp, err := util.DoWithRetry(s.client, req, 3, 500*time.Millisecond)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

func parseChapterLabel(href, title string) (main int, typ string, sub int, label string, ok bool) {
	h := strings.ToLower(href)
	t := strings.ToLower(title)

	hasChapterKeyword :=
		strings.Contains(h, "ch") ||
			strings.Contains(h, "chapter") ||
			strings.Contains(h, "vol") ||
			strings.Contains(t, "ch") ||
			strings.Contains(t, "chapter") ||
			strings.Contains(t, "vol")

	if !hasChapterKeyword {
		return 0, "", 0, "", false
	}

	if strings.Contains(h, "/u/") {
		return 0, "", 0, "", false
	}
	if strings.Contains(h, "batolists") {
		return 0, "", 0, "", false
	}
	if strings.Contains(h, "/title/") && !strings.Contains(h, "ch") && !strings.Contains(h, "vol") {
		return 0, "", 0, "", false
	}

	if m := chapterDash.FindStringSubmatch(href); m != nil {
		main, _ := strconv.Atoi(m[1])
		if m[2] != "" {
			sub, _ := strconv.Atoi(m[2])
			return main, "-", sub, fmt.Sprintf("%d-%d", main, sub), true
		}
		return main, "", 0, fmt.Sprintf("%d", main), true
	}

	if m := batoVol.FindStringSubmatch(h); m != nil {
		vol, _ := strconv.Atoi(m[1])
		ch, _ := strconv.Atoi(m[2])
		return ch, ".", vol, fmt.Sprintf("%d.%d", vol, ch), true
	}

	if m := batoSimple.FindStringSubmatch(href); m != nil {
		parts := strings.Split(m[1], ".")
		main, _ := strconv.Atoi(parts[0])

		if len(parts) == 2 {
			sub, _ := strconv.Atoi(parts[1])
			return main, ".", sub, fmt.Sprintf("%d.%d", main, sub), true
		}

		return main, "", 0, fmt.Sprintf("%d", main), true
	}

	if m := batoPlain.FindStringSubmatch(h); m != nil {
		if strings.Contains(h, "vol") {
			goto skipPlain
		}
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n, "", 0, m[1], true
		}
	}

skipPlain:
	if m := titlePrefix.FindStringSubmatch(title); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, "", 0, m[1], true
	}

	if m := chapRe.FindStringSubmatch(title); m != nil {
		main, _ = strconv.Atoi(m[1])
		typ = m[2]
		sub, _ = strconv.Atoi(m[3])

		switch typ {
		case ".":
			label = fmt.Sprintf("%d.%d", main, sub)
		case "-":
			label = fmt.Sprintf("%d-%d", main, sub)
		default:
			label = fmt.Sprintf("%d", main)
		}

		return main, typ, sub, label, true
	}

	return 0, "", 0, "", false
}

func looksLikeChapterLink(href, title string) bool {
	h := strings.ToLower(href)
	t := strings.ToLower(title)

	if reLikelyChapter.MatchString(h) {
		return true
	}

	if batoVol.MatchString(h) || batoSimple.MatchString(h) {
		return true
	}

	if strings.HasPrefix(t, "ch ") || strings.HasPrefix(t, "chapter ") {
		return true
	}

	return false
}

func resolveURL(base, href string) string {
	if href == "" {
		return base
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if u.IsAbs() {
		return u.String()
	}

	b, err := url.Parse(base)
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

		u := resolveURL(pageURL, strings.TrimSpace(href))
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

		rank := func(t string) int {
			switch t {
			case "":
				return 0
			case ".":
				return 1
			default:
				return 2
			}
		}

		ri, rj := rank(out[i].SuffixType), rank(out[j].SuffixType)
		if ri != rj {
			return ri < rj
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

	var imgs []string
	seen := map[string]bool{}

	doc.Find("img").Each(func(_ int, img *goquery.Selection) {
		src, _ := img.Attr("src")

		if src == "" || strings.HasPrefix(src, "data:") {
			if v, ok := img.Attr("data-src"); ok {
				src = v
			}
		}

		src = strings.TrimSpace(src)
		if src == "" {
			return
		}

		u := resolveURL(chapterURL, src)
		lu := strings.ToLower(u)

		if disallowedExt.MatchString(lu) {
			return
		}
		if !s.allowed.MatchString(lu) {
			return
		}

		if !seen[u] {
			seen[u] = true
			imgs = append(imgs, u)
		}
	})

	doc.Find("[style]").Each(func(_ int, el *goquery.Selection) {
		style, _ := el.Attr("style")
		sstyle := strings.ToLower(style)

		if !strings.Contains(sstyle, "background-image") {
			return
		}

		re := regexp.MustCompile(`url\(([^)]+)\)`)
		matches := re.FindAllStringSubmatch(style, -1)

		for _, m := range matches {
			raw := strings.Trim(m[1], `"'`)
			if raw == "" || strings.HasPrefix(raw, "data:") {
				continue
			}

			u := resolveURL(chapterURL, raw)
			lu := strings.ToLower(u)

			if disallowedExt.MatchString(lu) {
				continue
			}
			if !s.allowed.MatchString(lu) {
				continue
			}

			if !seen[u] {
				seen[u] = true
				imgs = append(imgs, u)
			}
		}
	})

	body, err := s.fetchBody(ctx, chapterURL)
	if err == nil {
		reNuxt := regexp.MustCompile(`window\.__NUXT__\s*=\s*(\{.*?});`)
		match := reNuxt.FindStringSubmatch(body)
		if len(match) > 1 {
			jsonText := match[1]

			var raw map[string]any
			if json.Unmarshal([]byte(jsonText), &raw) == nil {
				images := s.extractImagesFromNuxt(raw)
				for _, u := range images {
					if !seen[u] {
						fmt.Println("DEBUG: NUXT IMG:", u)
						imgs = append(imgs, u)
						seen[u] = true
					}
				}
			}
		}
	}

	body, err = s.fetchBody(ctx, chapterURL)
	if err != nil {
		return nil, err
	}

	candidates := regexp.MustCompile(`https?://[^\s"'<>]+`).FindAllString(body, -1)
	for _, u := range candidates {
		lu := strings.ToLower(u)

		if disallowedExt.MatchString(lu) {
			continue
		}

		if s.allowed.MatchString(lu) && !seen[u] {
			seen[u] = true
			imgs = append(imgs, u)
		}
	}

	if len(imgs) == 0 {
		return nil, fmt.Errorf("no images found on page")
	}

	return imgs, nil
}

func (s *Scraper) extractImagesFromNuxt(raw map[string]any) []string {
	var out []string

	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {

		case map[string]any:
			for _, v2 := range x {
				walk(v2)
			}

		case []any:
			for _, v2 := range x {
				walk(v2)
			}

		case string:
			if s.allowed.MatchString(strings.ToLower(x)) {
				out = append(out, x)
			}
		}
	}

	walk(raw)
	return out
}

func normalizeExtList(list []string) []string {
	out := []string{}

	for _, ext := range list {
		ext = strings.ToLower(strings.TrimSpace(ext))
		ext = strings.TrimPrefix(ext, ".")

		if ext != "" {
			out = append(out, ext)
		}
	}

	return out
}

func buildExtRegex(exts []string) *regexp.Regexp {
	if len(exts) == 0 {
		return regexp.MustCompile(`$a`)
	}

	pattern := `(?i)\.(` + strings.Join(exts, "|") + `)$`

	return regexp.MustCompile(pattern)
}
