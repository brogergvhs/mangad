// Package madara scrapes WordPress Madara-theme sites, whose chapter lists
// load from an ajax endpoint rather than the manga page HTML. Images reuse the
// generic scraper.
package madara

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/brogergvhs/kaodoku/internal/providers"
	"github.com/brogergvhs/kaodoku/internal/providers/generic"
	"github.com/brogergvhs/kaodoku/internal/ui"
	"github.com/brogergvhs/kaodoku/internal/util"
)

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
	chapters, err := s.ajaxChapters(ctx, pageURL)
	if err == nil && len(chapters) > 0 {
		return chapters, nil
	}
	if err != nil && s.log != nil {
		s.log.Debugf("Madara ajax chapter list failed for %s: %v\n", pageURL, err)
	}
	return s.fallback.GetChapters(ctx, pageURL)
}

func (s *Scraper) GetImages(ctx context.Context, chapterURL string) ([]string, error) {
	return s.fallback.GetImages(ctx, chapterURL)
}

// ajaxChapters fetches the theme's chapter-list endpoint, which serves the
// list the manga page would otherwise load via JS.
func (s *Scraper) ajaxChapters(ctx context.Context, pageURL string) ([]providers.Chapter, error) {
	target := strings.TrimRight(pageURL, "/") + "/ajax/chapters/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Referer", pageURL)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	resp, err := util.DoWithRetry(s.client, req, 2, 500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	out := generic.ScanChapterLinks(doc, pageURL, s.log)
	if len(out) > 0 && s.log != nil {
		s.log.Infof("Loaded %d chapters from the Madara ajax endpoint for %s.\n", len(out), pageURL)
	}
	return out, nil
}
