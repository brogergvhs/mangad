package library

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/providers"
)

func TestRepositoryTitleAndMissingChapters(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo := NewRepository(db)
	title, err := repo.AddTitle(ctx, AddTitleParams{
		SourceURL:       "https://example.test/manga",
		DisplayTitle:    "Example Manga",
		Monitored:       true,
		RefreshInterval: "12h",
	})
	if err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}
	if title.ID == 0 {
		t.Fatal("AddTitle() ID = 0")
	}
	if !title.Monitored {
		t.Fatal("AddTitle() Monitored = false, want true")
	}

	inserted, err := repo.UpsertChapters(ctx, title.ID, []chapters.Chapter{
		{Chapter: providers.Chapter{URL: "https://example.test/ch-1", Title: "Chapter 1", Label: "1", NumMain: 1}},
		{Chapter: providers.Chapter{URL: "https://example.test/ch-2", Title: "Chapter 2", Label: "2", NumMain: 2}},
	})
	if err != nil {
		t.Fatalf("UpsertChapters() error = %v", err)
	}
	if inserted != 2 {
		t.Fatalf("UpsertChapters() = %d, want 2", inserted)
	}

	titles, err := repo.ListTitles(ctx)
	if err != nil {
		t.Fatalf("ListTitles() error = %v", err)
	}
	if len(titles) != 1 {
		t.Fatalf("ListTitles() len = %d, want 1", len(titles))
	}
	if titles[0].DiscoveredCount != 2 || titles[0].MissingCount != 2 {
		t.Fatalf("ListTitles() counts = discovered %d missing %d, want 2/2", titles[0].DiscoveredCount, titles[0].MissingCount)
	}

	var chapterID int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM chapters WHERE title_id = ? AND label = '1'`, title.ID).Scan(&chapterID); err != nil {
		t.Fatalf("query chapter id error = %v", err)
	}
	if err := repo.MarkDownloadStarted(ctx, chapterID); err != nil {
		t.Fatalf("MarkDownloadStarted() error = %v", err)
	}
	if err := repo.MarkDownloadCompleted(ctx, chapterID, "chapter-1.cbz", 123); err != nil {
		t.Fatalf("MarkDownloadCompleted() error = %v", err)
	}
	completed, err := repo.ListCompletedDownloads(ctx, title.ID)
	if err != nil {
		t.Fatalf("ListCompletedDownloads() error = %v", err)
	}
	if len(completed) != 1 || completed[0].OutputFile != "chapter-1.cbz" {
		t.Fatalf("ListCompletedDownloads() = %+v, want one chapter-1.cbz", completed)
	}

	missing, err := repo.ListMissingChapters(ctx, title.ID)
	if err != nil {
		t.Fatalf("ListMissingChapters() error = %v", err)
	}
	if len(missing) != 1 {
		t.Fatalf("ListMissingChapters() len = %d, want 1", len(missing))
	}
	if missing[0].Label != "2" {
		t.Fatalf("ListMissingChapters()[0].Label = %q, want 2", missing[0].Label)
	}

	byLabel, err := repo.GetChapterByLabel(ctx, title.ID, "2")
	if err != nil {
		t.Fatalf("GetChapterByLabel() error = %v", err)
	}
	if byLabel.Label != "2" {
		t.Fatalf("GetChapterByLabel().Label = %q, want 2", byLabel.Label)
	}
	if err := repo.MarkDownloadFailed(ctx, byLabel.ID, assertErr("boom")); err != nil {
		t.Fatalf("MarkDownloadFailed() error = %v", err)
	}
	if err := repo.MarkDownloadStarted(ctx, byLabel.ID); err != nil {
		t.Fatalf("MarkDownloadStarted(stale) error = %v", err)
	}
	reconciled, err := repo.ReconcileStartedDownloads(ctx)
	if err != nil {
		t.Fatalf("ReconcileStartedDownloads() error = %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("ReconcileStartedDownloads() = %d, want 1", reconciled)
	}
	var status string
	if err := db.QueryRowContext(ctx, `SELECT status FROM downloads WHERE chapter_id = ?`, byLabel.ID).Scan(&status); err != nil {
		t.Fatalf("query reconciled status error = %v", err)
	}
	if status != "failed" {
		t.Fatalf("reconciled status = %q, want failed", status)
	}

	if err := repo.RemoveTitle(ctx, title.ID); err != nil {
		t.Fatalf("RemoveTitle() error = %v", err)
	}
	titles, err = repo.ListTitles(ctx)
	if err != nil {
		t.Fatalf("ListTitles() after remove error = %v", err)
	}
	if len(titles) != 0 {
		t.Fatalf("ListTitles() after remove len = %d, want 0", len(titles))
	}

	var chapterCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chapters WHERE title_id = ?`, title.ID).Scan(&chapterCount); err != nil {
		t.Fatalf("query chapter count after remove error = %v", err)
	}
	if chapterCount != 0 {
		t.Fatalf("chapter count after remove = %d, want 0", chapterCount)
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}
