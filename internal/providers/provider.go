package providers

import (
	"context"
	"net/url"
	"sort"
)

type Chapter struct {
	URL        string
	Title      string
	NumMain    int
	SuffixType string
	SuffixNum  int
	Label      string
}

// SortChapters orders chapters by main number, then suffix type and number.
func SortChapters(chapters []Chapter) {
	sort.SliceStable(chapters, func(i, j int) bool {
		if chapters[i].NumMain != chapters[j].NumMain {
			return chapters[i].NumMain < chapters[j].NumMain
		}
		if chapters[i].SuffixType != chapters[j].SuffixType {
			return chapters[i].SuffixType < chapters[j].SuffixType
		}
		return chapters[i].SuffixNum < chapters[j].SuffixNum
	})
}

type Scraper interface {
	GetChapters(ctx context.Context, url string) ([]Chapter, error)
	GetImages(ctx context.Context, chapterURL string) ([]string, error)
}

// RedirectAware is an optional Scraper capability: chapter fetches also
// report the URL the page resolved to after redirects, so callers can persist
// rotated source URLs (AsuraScans rotates a hash suffix on series slugs).
type RedirectAware interface {
	GetChaptersResolved(ctx context.Context, url string) ([]Chapter, string, error)
}

// Searcher is an optional Scraper capability for sources with a native search
// API (rather than a scrapeable HTML results page). searchURL supplies the
// host; it returns manga page URLs.
type Searcher interface {
	SearchManga(ctx context.Context, searchURL, query string) ([]string, error)
}

// ResolveURL resolves href against baseURL, tolerating malformed input.
func ResolveURL(baseURL, href string) string {
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
