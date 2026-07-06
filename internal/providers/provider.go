package providers

import (
	"context"
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
