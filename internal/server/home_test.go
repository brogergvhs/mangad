package server

import (
	"testing"
	"time"

	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/library"
)

func TestTopGenreWeighsByReads(t *testing.T) {
	titles := []library.Title{
		{ID: 1, ReadCount: 10},
		{ID: 2, ReadCount: 2, VolumeReadCount: 1},
		{ID: 3}, // unread: must not count
	}
	mangas := map[int64]catalog.Manga{
		1: {Genres: []string{"Action", "Fantasy"}},
		2: {Genres: []string{"Romance"}},
		3: {Genres: []string{"Romance", "Drama"}},
	}
	if got := topGenre(titles, mangas); got != "Action" {
		t.Fatalf("topGenre = %q, want Action", got)
	}
}

func TestContinueReadingRowOrderAndLinks(t *testing.T) {
	now := time.Now()
	titles := []library.Title{
		{ID: 1, DisplayTitle: "Chapters", ReadCount: 3, DiscoveredCount: 10},
		{ID: 2, DisplayTitle: "Volumes", VolumeReadCount: 1, VolumeCount: 3},
		{ID: 3, DisplayTitle: "Done", ReadCount: 5, DiscoveredCount: 5},
		{ID: 4, DisplayTitle: "Untouched", DiscoveredCount: 9},
	}
	lastRead := map[int64]time.Time{1: now.Add(-time.Hour), 2: now, 3: now}
	row := continueReadingRow(titles, lastRead)
	if len(row.Cards) != 2 {
		t.Fatalf("cards = %+v", row.Cards)
	}
	if row.Cards[0].Title != "Volumes" || row.Cards[0].Href != "/reader/2?mode=volumes" {
		t.Fatalf("first card = %+v", row.Cards[0])
	}
	if row.Cards[1].Href != "/reader/1" || row.Cards[1].Percent != 30 {
		t.Fatalf("second card = %+v", row.Cards[1])
	}
}
