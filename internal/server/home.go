package server

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/brogergvhs/mangad/internal/auth"
	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/library"
)

const homeRowLimit = 12

type homeCard struct {
	Href    string
	Cover   string
	Title   string
	Sub     string
	Percent int64 // -1 hides the bar
}

type homeRow struct {
	Heading string
	Cards   []homeCard
}

type homeView struct {
	User *auth.User
	Rows []homeRow
}

func (u *webUI) homePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	view := homeView{User: userFrom(ctx)}
	if view.User.Can(auth.PermLibraryView) {
		titles, _ := u.svc.ListTitles(ctx)
		titles = filterRestrictedTitles(ctx, titles)
		lastRead, _ := u.svc.LastReadAt(ctx)
		arrivals, _ := u.svc.LatestArrivals(ctx)
		mangas := u.titleMangas(ctx, titles)

		if row := continueReadingRow(titles, lastRead); len(row.Cards) > 0 {
			view.Rows = append(view.Rows, row)
		}
		if row := latestArrivalsRow("Latest arrivals", titles, nil); len(row.Cards) > 0 {
			view.Rows = append(view.Rows, row)
		}
		if row := latestUpdatesRow("Latest updates", titles, arrivals, nil); len(row.Cards) > 0 {
			view.Rows = append(view.Rows, row)
		}
		if genre := topGenre(titles, mangas); genre != "" {
			only := genreTitleIDs(mangas, genre)
			if row := latestArrivalsRow("Latest in "+genre, titles, only); len(row.Cards) > 0 {
				view.Rows = append(view.Rows, row)
			}
		}
	}
	u.page(w, r, "home", "Home", view)
}

func (u *webUI) titleMangas(ctx context.Context, titles []library.Title) map[int64]catalog.Manga {
	ids := make([]int64, 0, len(titles))
	for _, t := range titles {
		if t.CatalogMangaID != nil {
			ids = append(ids, *t.CatalogMangaID)
		}
	}
	byCatalog, _ := u.svc.MangaByIDs(ctx, ids)
	out := make(map[int64]catalog.Manga, len(byCatalog))
	for _, t := range titles {
		if t.CatalogMangaID != nil {
			if m, ok := byCatalog[*t.CatalogMangaID]; ok {
				out[t.ID] = m
			}
		}
	}
	return out
}

type timedCard struct {
	card homeCard
	at   time.Time
}

// topCards sorts newest-first and keeps up to homeRowLimit cards.
func topCards(entries []timedCard) []homeCard {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].at.After(entries[j].at) })
	out := make([]homeCard, 0, homeRowLimit)
	for i, e := range entries {
		if i == homeRowLimit {
			break
		}
		out = append(out, e.card)
	}
	return out
}

// continueReadingRow lists titles with partial progress, most recent first.
func continueReadingRow(titles []library.Title, lastRead map[int64]time.Time) homeRow {
	var entries []timedCard
	for _, t := range titles {
		chapters := t.ReadCount > 0 && t.ReadCount < t.DiscoveredCount
		volumes := t.VolumeReadCount > 0 && t.VolumeReadCount < t.VolumeCount
		if !chapters && !volumes {
			continue
		}
		at, ok := lastRead[t.ID]
		if !ok {
			continue
		}
		card := homeCard{Cover: t.CoverImage, Title: t.DisplayTitle}
		id := strconv.FormatInt(t.ID, 10)
		if chapters {
			card.Href = "/reader/" + id
			card.Sub = strconv.FormatInt(t.ReadCount, 10) + "/" + strconv.FormatInt(t.DiscoveredCount, 10) + " chapters"
			card.Percent = pctInt(t.ReadCount, t.DiscoveredCount)
		} else {
			card.Href = "/reader/" + id + "?mode=volumes"
			card.Sub = strconv.FormatInt(t.VolumeReadCount, 10) + "/" + strconv.FormatInt(t.VolumeCount, 10) + " volumes"
			card.Percent = pctInt(t.VolumeReadCount, t.VolumeCount)
		}
		entries = append(entries, timedCard{card: card, at: at})
	}
	return homeRow{Heading: "Continue reading", Cards: topCards(entries)}
}

// latestArrivalsRow lists titles by when they were added; `only` restricts the set.
func latestArrivalsRow(heading string, titles []library.Title, only map[int64]bool) homeRow {
	var entries []timedCard
	for _, t := range titles {
		if only != nil && !only[t.ID] {
			continue
		}
		sub := "Added"
		if rel := since(t.CreatedAt); rel != "" {
			sub = "Added " + rel
		}
		entries = append(entries, timedCard{at: t.CreatedAt, card: homeCard{
			Href:    "/library/" + strconv.FormatInt(t.ID, 10),
			Cover:   t.CoverImage,
			Title:   t.DisplayTitle,
			Sub:     sub,
			Percent: -1,
		}})
	}
	return homeRow{Heading: heading, Cards: topCards(entries)}
}

// latestUpdatesRow lists titles by their newest downloaded chapter or volume;
// `only` restricts the set.
func latestUpdatesRow(heading string, titles []library.Title, arrivals map[int64]library.Arrival, only map[int64]bool) homeRow {
	var entries []timedCard
	for _, t := range titles {
		if only != nil && !only[t.ID] {
			continue
		}
		arrival, ok := arrivals[t.ID]
		if !ok {
			continue
		}
		entries = append(entries, timedCard{at: arrival.At, card: homeCard{
			Href:    "/library/" + strconv.FormatInt(t.ID, 10),
			Cover:   t.CoverImage,
			Title:   t.DisplayTitle,
			Sub:     arrival.Label,
			Percent: -1,
		}})
	}
	return homeRow{Heading: heading, Cards: topCards(entries)}
}

// topGenre weighs each title's catalog genres by the user's read counts.
func topGenre(titles []library.Title, mangas map[int64]catalog.Manga) string {
	weights := map[string]int64{}
	for _, t := range titles {
		reads := t.ReadCount + t.VolumeReadCount
		if reads == 0 {
			continue
		}
		for _, g := range mangas[t.ID].Genres {
			weights[g] += reads
		}
	}
	best, bestWeight := "", int64(0)
	for g, w := range weights {
		if w > bestWeight || (w == bestWeight && g < best) {
			best, bestWeight = g, w
		}
	}
	return best
}

func genreTitleIDs(mangas map[int64]catalog.Manga, genre string) map[int64]bool {
	out := map[int64]bool{}
	for id, m := range mangas {
		for _, g := range m.Genres {
			if g == genre {
				out[id] = true
				break
			}
		}
	}
	return out
}

func pctInt(part, total int64) int64 {
	if total <= 0 {
		return 0
	}
	return part * 100 / total
}
