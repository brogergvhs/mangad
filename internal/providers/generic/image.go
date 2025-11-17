package generic

import (
	"fmt"
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
	reParseSize  = regexp.MustCompile(`[-_](\d{2,5})x(\d{2,5})(?:\.[A-Za-z0-9]+)?$`)

	reBackgroundURL = regexp.MustCompile(`url\((?:["']?)([^"')]+)(?:["']?)\)`)
	reLooseURLs     = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

type collectedItem struct {
	URL   string
	Index int // -1 if none
	Order int // monotonically increasing discovery order
}

type imageCollector struct {
	allowed *regexp.Regexp
	debug   bool
	items   []collectedItem
	seen    map[string]bool
	counter int
}

func newImageCollector(allowed *regexp.Regexp, debug bool) *imageCollector {
	return &imageCollector{
		allowed: allowed,
		debug:   debug,
		items:   make([]collectedItem, 0, 64),
		seen:    make(map[string]bool),
		counter: 0,
	}
}

func (c *imageCollector) add(url string, idx int) {
	if url == "" || strings.HasPrefix(url, "javascript:") {
		return
	}
	lu := strings.ToLower(url)
	if !c.allowed.MatchString(lu) {
		return
	}
	if strings.HasPrefix(lu, "data:") {
		return
	}
	if strings.Contains(lu, "logo") ||
		strings.Contains(lu, "cover") ||
		strings.Contains(lu, "profile") ||
		strings.Contains(lu, "avatar") ||
		strings.Contains(lu, "banner") {
		if c.debug {
			fmt.Printf("Skipping non-page image: %s\n", url)
		}

		return
	}
	if c.seen[url] {
		return
	}
	c.seen[url] = true
	c.counter++
	c.items = append(c.items, collectedItem{
		URL:   url,
		Index: idx,       // -1 if not known
		Order: c.counter, // discovery sequence
	})
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
	if err != nil || u == nil {
		return raw
	}

	if u.IsAbs() {
		return u.String()
	}

	base, err := url.Parse(chapterURL)
	if err != nil || base == nil {
		return raw
	}

	return base.ResolveReference(u).String()
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

func getIndexFor(sel *goquery.Selection) int {
	if v, ok := sel.Attr("data-index"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}

	p := sel.ParentsFiltered("[data-index]").First()
	if p.Length() > 0 {
		if v, ok := p.Attr("data-index"); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return n
			}
		}
	}

	return -1
}

func (c *imageCollector) ScanIMGTags(doc *goquery.Document, chapterURL string) int {
	before := len(c.items)
	doc.Find("img").Each(func(_ int, img *goquery.Selection) {
		idx := getIndexFor(img)

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

				c.add(resolve(chapterURL, parts[0]), idx)
			}
		}

		for _, k := range []string{"src", "data-src", "data-lazy-src", "data-original"} {
			if v, ok := img.Attr(k); ok && strings.TrimSpace(v) != "" {
				c.add(resolve(chapterURL, v), idx)
			}
		}
	})

	return len(c.items) - before
}

func (c *imageCollector) ScanBackgroundImages(doc *goquery.Document, chapterURL string) int {
	before := len(c.items)
	doc.Find("[style]").Each(func(_ int, el *goquery.Selection) {
		style, _ := el.Attr("style")
		if !strings.Contains(strings.ToLower(style), "background-image") {
			return
		}

		idx := getIndexFor(el)
		for _, m := range reBackgroundURL.FindAllStringSubmatch(style, -1) {
			u := strings.TrimSpace(m[1])
			if u != "" {
				c.add(resolve(chapterURL, u), idx)
			}
		}
	})

	return len(c.items) - before
}

func (c *imageCollector) ScanAnchorImages(doc *goquery.Document, chapterURL string) int {
	before := len(c.items)
	doc.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		href, ok := a.Attr("href")
		if !ok {
			return
		}

		href = strings.TrimSpace(href)
		if href == "" {
			return
		}

		if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") ||
			strings.HasPrefix(href, "/") || strings.HasPrefix(href, "./") {
			idx := getIndexFor(a)
			c.add(resolve(chapterURL, href), idx)
		}
	})

	return len(c.items) - before
}

func (c *imageCollector) ScanPictureSources(doc *goquery.Document, chapterURL string) int {
	before := len(c.items)

	doc.Find("source[srcset]").Each(func(_ int, src *goquery.Selection) {
		idx := getIndexFor(src)

		if ss, ok := src.Attr("srcset"); ok && strings.TrimSpace(ss) != "" {
			for p := range strings.SplitSeq(ss, ",") {
				p = strings.TrimSpace(p)
				if p == "" {
					continue
				}

				parts := strings.Fields(p)
				if len(parts) == 0 {
					continue
				}

				c.add(resolve(chapterURL, parts[0]), idx)
			}
		}
	})

	return len(c.items) - before
}

func (c *imageCollector) ScanNuxt(root map[string]any, chapterURL string) {
	var walk func(v any)

	walk = func(v any) {
		switch t := v.(type) {
		case string:
			s := strings.TrimSpace(t)
			ls := strings.ToLower(s)
			if strings.HasPrefix(ls, "http://") || strings.HasPrefix(ls, "https://") {
				if reExt.MatchString(ls) {
					c.add(s, -1)
				}

				return
			}
			if looksLikeHTML(s) {
				if doc, err := goquery.NewDocumentFromReader(strings.NewReader(s)); err == nil {
					c.ScanIMGTags(doc, chapterURL)
					c.ScanPictureSources(doc, chapterURL)
					c.ScanAnchorImages(doc, chapterURL)
					c.ScanBackgroundImages(doc, chapterURL)
				}
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

	for _, u := range reLooseURLs.FindAllString(body, -1) {
		c.add(u, -1)
	}
}

func (c *imageCollector) Finalize() []string {
	if len(c.items) == 0 {
		return nil
	}

	groups := groupCollectedItems(c.items)
	chosenList := chooseBestImages(groups)
	sortChosen(chosenList)

	out := make([]string, len(chosenList))
	for i := range chosenList {
		out[i] = chosenList[i].URL
	}

	return out
}

// groupCollectedItems groups collected images by their normalized base URL.
func groupCollectedItems(items []collectedItem) map[string][]collectedItem {
	type grp struct {
		firstOrder int
		items      []collectedItem
	}
	groups := map[string]*grp{}

	for _, it := range items {
		key := normalizeBase(it.URL)
		g, ok := groups[key]
		if !ok {
			g = &grp{firstOrder: it.Order}
			groups[key] = g
		}

		if it.Order < g.firstOrder {
			g.firstOrder = it.Order
		}

		g.items = append(g.items, it)
	}

	out := make(map[string][]collectedItem, len(groups))
	for k, g := range groups {
		out[k] = g.items
	}

	return out
}

// chooseBestImages selects the best candidate per group.
func chooseBestImages(groups map[string][]collectedItem) []chosenItem {
	chosenList := make([]chosenItem, 0, len(groups))

	for _, items := range groups {
		picked := pickBestItem(items)

		finalIdx, minOrder := deriveIndexAndOrder(items, picked)
		chosenList = append(chosenList, chosenItem{
			URL:   picked.URL,
			Index: finalIdx,
			Order: minOrder,
		})
	}

	return chosenList
}

type chosenItem struct {
	URL   string
	Index int
	Order int
}

// pickBestItem returns the preferred image within one group.
func pickBestItem(items []collectedItem) collectedItem {
	var noSuffix, dimens []collectedItem

	for _, it := range items {
		if reSizeSuffix.MatchString(it.URL) {
			dimens = append(dimens, it)
		} else {
			noSuffix = append(noSuffix, it)
		}
	}

	switch {
	case len(noSuffix) > 0:
		sort.SliceStable(noSuffix, func(i, j int) bool { return noSuffix[i].Order < noSuffix[j].Order })
		return noSuffix[0]

	case len(dimens) > 0:
		best := dimens[0]
		bw, bh := parseWxH(best.URL)
		bestArea := bw * bh
		for _, it := range dimens[1:] {
			w, h := parseWxH(it.URL)
			if w*h > bestArea {
				best = it
				bestArea = w * h
			}
		}
		return best

	default:
		return items[0]
	}
}

// deriveIndexAndOrder decides the final index and earliest discovery order.
func deriveIndexAndOrder(items []collectedItem, picked collectedItem) (finalIdx, minOrder int) {
	finalIdx = -1
	minOrder = picked.Order

	for _, it := range items {
		if it.Index >= 0 && (finalIdx < 0 || it.Index < finalIdx) {
			finalIdx = it.Index
		}

		if it.Order < minOrder {
			minOrder = it.Order
		}
	}

	return
}

// sortChosen sorts the chosen list according to the rules described.
func sortChosen(list []chosenItem) {
	sort.SliceStable(list, func(i, j int) bool {
		ai, aj := list[i].Index, list[j].Index
		if ai >= 0 && aj >= 0 && ai != aj {
			return ai < aj
		}
		if ai >= 0 && aj < 0 {
			return true
		}
		if ai < 0 && aj >= 0 {
			return false
		}

		return list[i].Order < list[j].Order
	})
}
