package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brogergvhs/kaodoku/internal/catalog"
	"github.com/brogergvhs/kaodoku/internal/chapters"
	"github.com/brogergvhs/kaodoku/internal/library"
	"github.com/brogergvhs/kaodoku/internal/providers"
	"github.com/brogergvhs/kaodoku/internal/util"
)

func TestImportedChapterMetaPrefersComicInfo(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "p.jpg")
	if err := os.WriteFile(img, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := util.MarshalComicInfo(util.ComicInfo{Number: "7.5", Title: "Lost Children"})
	if err := util.CreateCBZ([]string{img}, filepath.Join(dir, "scan_042.cbz"), false,
		map[string][]byte{util.ComicInfoName: body}); err != nil {
		t.Fatal(err)
	}
	if err := util.CreateCBZ([]string{img}, filepath.Join(dir, "Chapter 003.cbz"), false, nil); err != nil {
		t.Fatal(err)
	}

	label, num, title := importedChapterMeta(dir, "scan_042.cbz")
	if label != "7.5" || num != 7 || title != "Lost Children" {
		t.Fatalf("comicinfo meta = %q/%d/%q", label, num, title)
	}
	label, num, title = importedChapterMeta(dir, "Chapter 003.cbz")
	if label != "3" || num != 3 || title != "Chapter 003" {
		t.Fatalf("fallback meta = %q/%d/%q", label, num, title)
	}
}

func TestComicInfoTemplateAndEntry(t *testing.T) {
	ch := 120
	catalogID := int64(9)
	lib := &LibraryService{}
	lib.SetMangaLookup(func(_ context.Context, id int64) (catalog.Manga, error) {
		if id != catalogID {
			t.Fatalf("catalog id = %d", id)
		}
		return catalog.Manga{
			Description: "<b>Dark</b> fantasy", Authors: []string{"Miura"},
			Genres: []string{"Action", "Horror"}, Chapters: &ch, Year: 1990, IsAdult: true,
		}, nil
	})
	title := library.Title{DisplayTitle: "Berserk", SourceURL: "https://x.test/berserk", CatalogMangaID: &catalogID}
	ci := lib.comicInfoTemplate(context.Background(), title)
	if ci.Series != "Berserk" || ci.Writer != "Miura" || ci.Genre != "Action, Horror" ||
		ci.Count != 120 || ci.Year != 1990 || ci.AgeRating != "Adults Only 18+" ||
		ci.Summary != "Dark fantasy" || ci.Manga != "Yes" {
		t.Fatalf("template = %+v", ci)
	}

	dl := &DownloadService{comicInfo: ci}
	extra := dl.comicInfoEntry(chapters.Chapter{Chapter: providers.Chapter{Label: "364", Title: "The End"}}, 20)
	body := string(extra[util.ComicInfoName])
	for _, want := range []string{"<Number>364</Number>", "<Title>The End</Title>", "<PageCount>20</PageCount>", "<Series>Berserk</Series>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("entry missing %s: %s", want, body)
		}
	}
	minimal := string((&DownloadService{}).comicInfoEntry(
		chapters.Chapter{Chapter: providers.Chapter{Label: "3"}}, 5)[util.ComicInfoName])
	if !strings.Contains(minimal, "<Number>3</Number>") || !strings.Contains(minimal, "<Manga>Yes</Manga>") ||
		strings.Contains(minimal, "<Series>") {
		t.Fatalf("minimal entry = %s", minimal)
	}

	bare := lib.comicInfoTemplate(context.Background(), library.Title{DisplayTitle: "NoCatalog"})
	if bare.Series != "NoCatalog" || bare.Summary != "" {
		t.Fatalf("bare template = %+v", bare)
	}
}
