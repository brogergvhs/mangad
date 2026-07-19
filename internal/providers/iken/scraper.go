// Package iken implements the "Iken" site template's JSON API (VortexScans
// and similar sites). The series page HTML only renders the newest ~20
// chapters behind a "Show more" button; the full list lives on the api.
// subdomain. Images reuse the generic scraper.
package iken

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/brogergvhs/kaodoku/internal/providers"
	"github.com/brogergvhs/kaodoku/internal/providers/generic"
	"github.com/brogergvhs/kaodoku/internal/ui"
	"github.com/brogergvhs/kaodoku/internal/util"
)

const (
	chapterPageSize = 200
	maxChapterPages = 100
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
	chapters, err := s.fetchAPIChapters(ctx, pageURL)
	if err == nil && len(chapters) > 0 {
		return chapters, nil
	}
	if err != nil && s.log != nil {
		s.log.Debugf("Iken chapter API failed for %s: %v\n", pageURL, err)
	}
	return s.fallback.GetChapters(ctx, pageURL)
}

func (s *Scraper) GetImages(ctx context.Context, chapterURL string) ([]string, error) {
	return s.fallback.GetImages(ctx, chapterURL)
}

// SearchManga queries the template's search API and returns series page URLs
// on the site host (the API lives on the api. subdomain).
func (s *Scraper) SearchManga(ctx context.Context, searchURL, query string) ([]string, error) {
	u, err := url.Parse(strings.ReplaceAll(searchURL, "{query}", url.QueryEscape(query)))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid search URL %q", searchURL)
	}
	if !strings.Contains(searchURL, "{query}") {
		u.Path = "/api/query"
		u.RawQuery = url.Values{"searchTerm": []string{query}, "perPage": []string{"10"}}.Encode()
	}
	var out struct {
		Posts []struct {
			Slug string `json:"slug"`
		} `json:"posts"`
	}
	if err := util.GetJSON(ctx, s.client, u.String(), &out); err != nil {
		return nil, err
	}
	site := strings.TrimPrefix(u.Host, "api.")
	var urls []string
	for _, p := range out.Posts {
		if p.Slug != "" {
			urls = append(urls, fmt.Sprintf("%s://%s/series/%s", u.Scheme, site, p.Slug))
		}
	}
	return urls, nil
}

func (s *Scraper) fetchAPIChapters(ctx context.Context, pageURL string) ([]providers.Chapter, error) {
	page, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}
	slug, err := seriesSlug(page)
	if err != nil {
		return nil, err
	}
	api, postID, total, err := s.resolvePost(ctx, page, slug)
	if err != nil {
		return nil, err
	}

	var out []providers.Chapter
	skipped := 0
	for pageNum := 0; pageNum < maxChapterPages; pageNum++ {
		var resp struct {
			Post struct {
				Chapters []chapterRow `json:"chapters"`
			} `json:"post"`
			TotalChapterCount int `json:"totalChapterCount"`
		}
		listURL := fmt.Sprintf("%s/api/chapters?postId=%d&skip=%d&take=%d&order=desc", api, postID, pageNum*chapterPageSize, chapterPageSize)
		if err := util.GetJSON(ctx, s.client, listURL, &resp); err != nil {
			return nil, err
		}
		if resp.TotalChapterCount > total {
			total = resp.TotalChapterCount
		}
		if len(resp.Post.Chapters) == 0 {
			break
		}
		for _, row := range resp.Post.Chapters {
			if row.Slug == "" {
				continue
			}
			if row.IsAccessible != nil && !*row.IsAccessible {
				skipped++ // paid/locked chapter: listing it would only fail downloads
				continue
			}
			main, suffixType, suffixNum, label, ok := providers.ParseChapterNumber(row.Number.String())
			if !ok {
				continue
			}
			title := "Chapter " + label
			if extra := strings.TrimSpace(row.Title); extra != "" {
				title += " - " + extra
			}
			out = append(out, providers.Chapter{
				URL:        strings.TrimRight(pageURL, "/") + "/" + row.Slug,
				Title:      title,
				NumMain:    main,
				SuffixType: suffixType,
				SuffixNum:  suffixNum,
				Label:      label,
			})
		}
		if (pageNum+1)*chapterPageSize >= total {
			break
		}
	}
	if skipped > 0 && s.log != nil {
		s.log.Infof("Skipped %d locked chapters on %s.\n", skipped, pageURL)
	}
	providers.SortChapters(out)
	return out, nil
}

type chapterRow struct {
	Slug         string      `json:"slug"`
	Number       json.Number `json:"number"`
	Title        string      `json:"title"`
	IsAccessible *bool       `json:"isAccessible"`
}

// resolvePost finds the API base and numeric post id for a series slug. The
// API conventionally lives on the api. subdomain; same-host /api is the
// fallback for deployments without one.
func (s *Scraper) resolvePost(ctx context.Context, page *url.URL, slug string) (api string, postID, total int, err error) {
	hosts := []string{"api." + page.Host, page.Host}
	if strings.HasPrefix(page.Host, "api.") {
		hosts = []string{page.Host}
	}
	var lastErr error
	for _, host := range hosts {
		base := page.Scheme + "://" + host
		var resp struct {
			TotalChapterCount int `json:"totalChapterCount"`
			Post              struct {
				ID int `json:"id"`
			} `json:"post"`
		}
		if err := util.GetJSON(ctx, s.client, base+"/api/post?postSlug="+url.QueryEscape(slug), &resp); err != nil {
			lastErr = err
			continue
		}
		if resp.Post.ID > 0 {
			return base, resp.Post.ID, resp.TotalChapterCount, nil
		}
		lastErr = fmt.Errorf("post %q not found on %s", slug, host)
	}
	return "", 0, 0, lastErr
}

func seriesSlug(page *url.URL) (string, error) {
	parts := strings.Split(strings.Trim(page.Path, "/"), "/")
	if len(parts) < 2 || parts[0] != "series" {
		return "", fmt.Errorf("not an Iken series URL")
	}
	return parts[1], nil
}
