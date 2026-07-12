package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const anilistTokenURL = "https://anilist.co/api/v2/oauth/token"

type ctxTokenKey struct{}

// WithToken carries a user's AniList access token; the client attaches it to
// requests, raising rate limits and unlocking user-scoped fields.
func WithToken(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxTokenKey{}, token)
}

// TokenFromContext returns the AniList token carried by the context, if any.
func TokenFromContext(ctx context.Context) string {
	t, _ := ctx.Value(ctxTokenKey{}).(string)
	return t
}

// AniListExchangeCode swaps an authorization code for a long-lived token.
func AniListExchangeCode(ctx context.Context, clientID, clientSecret, redirectURI, code string) (token string, expiresIn int, err error) {
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"client_secret": clientSecret,
		"redirect_uri":  redirectURI,
		"code":          code,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anilistTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("anilist token exchange failed (%d): %s", resp.StatusCode, data)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.AccessToken == "" {
		return "", 0, fmt.Errorf("anilist token exchange: unexpected response")
	}
	return out.AccessToken, out.ExpiresIn, nil
}

// AniListViewer returns the authenticated account's identity.
func AniListViewer(ctx context.Context, token string) (id int, name string, err error) {
	body, _ := json.Marshal(map[string]any{"query": `query { Viewer { id name } }`})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://graphql.anilist.co", bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	var out struct {
		Data struct {
			Viewer struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"Viewer"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return 0, "", err
	}
	if out.Data.Viewer.ID == 0 {
		return 0, "", fmt.Errorf("anilist viewer lookup failed")
	}
	return out.Data.Viewer.ID, out.Data.Viewer.Name, nil
}

// AniListEntry is one manga on a user's AniList list.
type AniListEntry struct {
	Manga    Manga
	Progress int    // chapters read on AniList
	Status   string // CURRENT, COMPLETED, PLANNING, ...
}

// UserList returns the user's AniList manga list (authenticated via ctx token).
func (c *AniListClient) UserList(ctx context.Context, anilistUserID int) ([]AniListEntry, error) {
	var resp struct {
		Data struct {
			MediaListCollection struct {
				Lists []struct {
					Entries []struct {
						Progress int          `json:"progress"`
						Status   string       `json:"status"`
						Media    anilistMedia `json:"media"`
					} `json:"entries"`
				} `json:"lists"`
			} `json:"MediaListCollection"`
		} `json:"data"`
	}
	if err := c.do(ctx, `
		query ($userId: Int) {
			MediaListCollection(userId: $userId, type: MANGA) {
				lists { entries { progress status media {
					id title { romaji english native } description(asHtml: false)
					coverImage { large } status format chapters volumes synonyms genres averageScore startDate { year } isAdult tags { name }
				} } }
			}
		}`, map[string]any{"userId": anilistUserID}, &resp); err != nil {
		return nil, err
	}
	var out []AniListEntry
	seen := map[string]bool{}
	for _, list := range resp.Data.MediaListCollection.Lists {
		for _, e := range list.Entries {
			items, err := anilistMediaToManga([]anilistMedia{e.Media})
			if err != nil || len(items) == 0 {
				continue
			}
			m := items[0]
			if m.Format != "MANGA" || seen[m.ProviderID] {
				continue
			}
			seen[m.ProviderID] = true
			out = append(out, AniListEntry{Manga: m, Progress: e.Progress, Status: e.Status})
		}
	}
	return out, nil
}

// MediaProgress returns the authenticated user's current progress on a media.
func (c *AniListClient) MediaProgress(ctx context.Context, mediaID int) (int, error) {
	var resp struct {
		Data struct {
			Media struct {
				MediaListEntry *struct {
					Progress int `json:"progress"`
				} `json:"mediaListEntry"`
			} `json:"Media"`
		} `json:"data"`
	}
	if err := c.do(ctx, `
		query ($id: Int) { Media(id: $id, type: MANGA) { mediaListEntry { progress } } }
	`, map[string]any{"id": mediaID}, &resp); err != nil {
		return 0, err
	}
	if resp.Data.Media.MediaListEntry == nil {
		return 0, nil
	}
	return resp.Data.Media.MediaListEntry.Progress, nil
}

// SaveProgress sets the authenticated user's chapter progress on a media.
func (c *AniListClient) SaveProgress(ctx context.Context, mediaID, progress int) error {
	var resp struct {
		Data struct {
			SaveMediaListEntry struct {
				ID int `json:"id"`
			} `json:"SaveMediaListEntry"`
		} `json:"data"`
	}
	return c.do(ctx, `
		mutation ($id: Int, $progress: Int) {
			SaveMediaListEntry(mediaId: $id, progress: $progress) { id }
		}`, map[string]any{"id": mediaID, "progress": progress}, &resp)
}
