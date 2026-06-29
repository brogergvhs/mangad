package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const AniListProvider = "anilist"

// AniListClient queries AniList GraphQL.
type AniListClient struct {
	endpoint string
	client   *http.Client
}

// NewAniListClient creates an AniList client.
func NewAniListClient(client *http.Client) *AniListClient {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &AniListClient{endpoint: "https://graphql.anilist.co", client: client}
}

// Search returns manga results for a text query.
func (c *AniListClient) Search(ctx context.Context, query string, limit int) ([]Manga, error) {
	if limit <= 0 {
		limit = 10
	}
	var resp anilistSearchResponse
	if err := c.do(ctx, `
		query ($search: String, $perPage: Int) {
			Page(page: 1, perPage: $perPage) {
				media(search: $search, type: MANGA) {
					id title { romaji english native } description(asHtml: false)
					coverImage { large } status format chapters synonyms
				}
			}
		}`, map[string]any{"search": query, "perPage": limit}, &resp); err != nil {
		return nil, err
	}
	return anilistMediaToManga(resp.Data.Page.Media)
}

// Get returns one AniList manga by ID.
func (c *AniListClient) Get(ctx context.Context, id int) (Manga, error) {
	var resp anilistGetResponse
	if err := c.do(ctx, `
		query ($id: Int) {
			Media(id: $id, type: MANGA) {
				id title { romaji english native } description(asHtml: false)
				coverImage { large } status format chapters synonyms
			}
		}`, map[string]any{"id": id}, &resp); err != nil {
		return Manga{}, err
	}
	items, err := anilistMediaToManga([]anilistMedia{resp.Data.Media})
	if err != nil {
		return Manga{}, err
	}
	if len(items) == 0 {
		return Manga{}, fmt.Errorf("anilist manga %d not found", id)
	}
	return items[0], nil
}

func (c *AniListClient) do(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("anilist request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("anilist HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode anilist response: %w", err)
	}
	return nil
}

type anilistSearchResponse struct {
	Data struct {
		Page struct {
			Media []anilistMedia `json:"media"`
		} `json:"Page"`
	} `json:"data"`
}

type anilistGetResponse struct {
	Data struct {
		Media anilistMedia `json:"Media"`
	} `json:"data"`
}

type anilistMedia struct {
	ID    int `json:"id"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"title"`
	Description string `json:"description"`
	CoverImage  struct {
		Large string `json:"large"`
	} `json:"coverImage"`
	Status   string   `json:"status"`
	Format   string   `json:"format"`
	Chapters *int     `json:"chapters"`
	Synonyms []string `json:"synonyms"`
}

func anilistMediaToManga(media []anilistMedia) ([]Manga, error) {
	out := make([]Manga, 0, len(media))
	for _, item := range media {
		if item.ID == 0 {
			continue
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		out = append(out, Manga{
			Provider:     AniListProvider,
			ProviderID:   strconv.Itoa(item.ID),
			TitleRomaji:  item.Title.Romaji,
			TitleEnglish: item.Title.English,
			TitleNative:  item.Title.Native,
			Description:  item.Description,
			CoverImage:   item.CoverImage.Large,
			Status:       item.Status,
			Format:       item.Format,
			Chapters:     item.Chapters,
			Synonyms:     item.Synonyms,
			RawJSON:      string(raw),
		})
	}
	return out, nil
}
