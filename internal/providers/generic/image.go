package generic

import (
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var (
	reExt = regexp.MustCompile(`(?i)\.(jpg|jpeg|png|webp)$`)

	reSizeSuffix = regexp.MustCompile(`[-_]\d{2,5}x\d{2,5}`)

	reBackgroundURL = regexp.MustCompile(`url\((?:["']?)([^"')]+)(?:["']?)\)`)
	reLooseURLs     = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

type imageCollector struct {
	allowed *regexp.Regexp
	raw     []string // first-seen order across all scans
}

func newImageCollector(allowed *regexp.Regexp) *imageCollector {
	return &imageCollector{allowed: allowed, raw: []string{}}
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

func resolve(chapterURL, raw string) string {
	u, err := url.Parse(raw)
	if err == nil && u.IsAbs() {
		return u.String()
	}

	base, err := url.Parse(chapterURL)
	if err == nil {
		return base.ResolveReference(u).String()
	}

	return raw
}

func normalizeBase(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	ext := path.Ext(u.Path)
	base := strings.TrimSuffix(u.Path, ext)

	base = reSizeSuffix.ReplaceAllString(base, "")
	base = strings.TrimRight(base, "-_")

	return base + ext
}

var reParseSize = regexp.MustCompile(`[-_](\d{2,5})x(\d{2,5})(?:\.[A-Za-z0-9]+)?$`)

func parseWxH(u string) (int, int) {
	if m := reParseSize.FindStringSubmatch(u); m != nil {
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		return w, h
	}

	if m := regexp.MustCompile(`[-_](\d{2,5})x(\d{2,5})`).FindStringSubmatch(u); m != nil {
		w, _ := strconv.Atoi(m[1])
		h, _ := strconv.Atoi(m[2])
		return w, h
	}

	return 0, 0
}

func (c *imageCollector) ScanIMGTags(doc *goquery.Document, chapterURL string) {
	doc.Find("img").Each(func(_ int, img *goquery.Selection) {
		if ss, ok := img.Attr("srcset"); ok && strings.TrimSpace(ss) != "" {
			for p := range strings.SplitSeq(ss, ",") {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}

				parts := strings.Fields(p)
				if len(parts) == 0 {
					continue
				}

				c.raw = append(c.raw, resolve(chapterURL, parts[0]))
			}
		}

		for _, k := range []string{"src", "data-src", "data-lazy-src", "data-original"} {
			if v, ok := img.Attr(k); ok && strings.TrimSpace(v) != "" {
				c.raw = append(c.raw, resolve(chapterURL, v))
			}
		}
	})
}

func (c *imageCollector) ScanBackgroundImages(doc *goquery.Document, chapterURL string) {
	doc.Find("[style]").Each(func(_ int, el *goquery.Selection) {
		style, _ := el.Attr("style")
		if !strings.Contains(strings.ToLower(style), "background-image") {
			return
		}

		for _, m := range reBackgroundURL.FindAllStringSubmatch(style, -1) {
			u := strings.TrimSpace(m[1])
			if u != "" {
				c.raw = append(c.raw, resolve(chapterURL, u))
			}
		}
	})
}

func (c *imageCollector) ScanNuxt(root map[string]any) {
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			if reExt.MatchString(strings.ToLower(t)) {
				c.raw = append(c.raw, t)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		}
	}

	walk(root)
}

func (c *imageCollector) ScanLooseURLs(body string) {
	if body == "" {
		return
	}

	c.raw = append(c.raw, reLooseURLs.FindAllString(body, -1)...)
}

func (c *imageCollector) Finalize() []string {
	seen := map[string]bool{}
	valid := []string{}

	for _, u := range c.raw {
		lu := strings.ToLower(u)

		if strings.HasPrefix(lu, "data:") {
			continue
		}

		if strings.Contains(lu, "logo") {
			continue
		}

		if !c.allowed.MatchString(lu) {
			continue
		}

		if !seen[u] {
			seen[u] = true
			valid = append(valid, u)
		}
	}

	if len(valid) == 0 {
		return nil
	}

	type group struct {
		firstIdx int
		noSuffix []string
		dimens   []string
	}
	groups := map[string]*group{}

	for i, u := range valid {
		key := normalizeBase(u)
		g, ok := groups[key]
		if !ok {
			g = &group{firstIdx: i}
			groups[key] = g
		}
		if i < g.firstIdx {
			g.firstIdx = i
		}

		if reSizeSuffix.MatchString(u) {
			g.dimens = append(g.dimens, u)
		} else {
			g.noSuffix = append(g.noSuffix, u)
		}
	}

	type chosen struct {
		idx int
		url string
	}

	chosenList := make([]chosen, 0, len(groups))

	for _, g := range groups {
		if len(g.noSuffix) > 0 {
			chosenList = append(chosenList, chosen{idx: g.firstIdx, url: g.noSuffix[0]})
			continue
		}

		if len(g.dimens) > 0 {
			best := g.dimens[0]
			bw, bh := parseWxH(best)
			bestArea := bw * bh

			for _, u := range g.dimens[1:] {
				w, h := parseWxH(u)
				area := w * h
				if area > bestArea {
					best = u
					bestArea = area
				}
			}

			chosenList = append(chosenList, chosen{idx: g.firstIdx, url: best})
		}
	}

	sort.SliceStable(chosenList, func(i, j int) bool {
		return chosenList[i].idx < chosenList[j].idx
	})

	out := make([]string, 0, len(chosenList))
	for _, c := range chosenList {
		out = append(out, c.url)
	}

	return out
}
