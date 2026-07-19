package library

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/brogergvhs/kaodoku/internal/database"
)

func TestListTitlesCatalogFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "kaodoku.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO catalog_manga (provider, provider_id, average_score, tags_json, genres_json)
		VALUES ('anilist', '123', 71, '["Magic"]', '["Action"]')`)
	if err != nil {
		t.Fatalf("insert catalog_manga: %v", err)
	}
	catalogID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	repo := NewRepository(db)
	if _, err := repo.AddTitle(ctx, AddTitleParams{
		CatalogMangaID: &catalogID,
		SourceURL:      "https://example.test/manga",
		DisplayTitle:   "Example Manga",
	}); err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}

	titles, err := repo.ListTitles(ctx)
	if err != nil {
		t.Fatalf("ListTitles() error = %v", err)
	}
	if len(titles) != 1 {
		t.Fatalf("ListTitles() len = %d, want 1", len(titles))
	}
	got := titles[0]
	if got.AverageScore != 71 {
		t.Errorf("AverageScore = %d, want 71", got.AverageScore)
	}
	if want := []string{"Magic", "Action"}; !reflect.DeepEqual(got.ContentTags, want) {
		t.Errorf("ContentTags = %v, want %v", got.ContentTags, want)
	}
}
