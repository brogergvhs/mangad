package providers

import "context"

type Chapter struct {
	URL        string
	Title      string
	NumMain    int
	SuffixType string
	SuffixNum  int
	Label      string
}

type Scraper interface {
	GetChapters(ctx context.Context, url string) ([]Chapter, error)
	GetImages(ctx context.Context, chapterURL string) ([]string, error)
}
