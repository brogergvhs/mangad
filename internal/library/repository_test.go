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
	if err := repo.MarkDownloadCompleted(ctx, chapterID, "chapter-1.cbz", 123, 20); err != nil {
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

func TestRepositoryDownloadAttemptCap(t *testing.T) {
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
		SourceURL:    "https://example.test/manga",
		DisplayTitle: "Example Manga",
		Monitored:    true,
	})
	if err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}
	if _, err := repo.UpsertChapters(ctx, title.ID, []chapters.Chapter{
		{Chapter: providers.Chapter{URL: "https://example.test/ch-1", Label: "1", NumMain: 1}},
	}); err != nil {
		t.Fatalf("UpsertChapters() error = %v", err)
	}
	missing, err := repo.ListMissingChapters(ctx, title.ID)
	if err != nil {
		t.Fatalf("ListMissingChapters() error = %v", err)
	}
	if len(missing) != 1 {
		t.Fatalf("missing = %d, want 1", len(missing))
	}
	chapterID := missing[0].ID

	for i := 0; i < DefaultDownloadAttempts; i++ {
		if err := repo.MarkDownloadStarted(ctx, chapterID); err != nil {
			t.Fatalf("MarkDownloadStarted() error = %v", err)
		}
		if err := repo.MarkDownloadFailed(ctx, chapterID, context.DeadlineExceeded); err != nil {
			t.Fatalf("MarkDownloadFailed() error = %v", err)
		}
		missing, err = repo.ListMissingChapters(ctx, title.ID)
		if err != nil {
			t.Fatalf("ListMissingChapters() error = %v", err)
		}
		want := 1
		if i == DefaultDownloadAttempts-1 {
			want = 0
		}
		if len(missing) != want {
			t.Fatalf("after %d failures missing = %d, want %d", i+1, len(missing), want)
		}
	}

	// A completed download resets the attempt counter.
	if err := repo.MarkDownloadCompleted(ctx, chapterID, "out.cbz", 1, 1); err != nil {
		t.Fatalf("MarkDownloadCompleted() error = %v", err)
	}
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM downloads WHERE chapter_id = ?`, chapterID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("attempts after completion = %d, want 0", attempts)
	}
}

func TestRepositoryReadProgress(t *testing.T) {
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
		SourceURL:    "https://example.test/manga",
		DisplayTitle: "Example Manga",
		Monitored:    true,
	})
	if err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}
	if _, err := repo.UpsertChapters(ctx, title.ID, []chapters.Chapter{
		{Chapter: providers.Chapter{URL: "https://example.test/ch-1", Label: "1", NumMain: 1}},
		{Chapter: providers.Chapter{URL: "https://example.test/ch-2", Label: "2", NumMain: 2}},
	}); err != nil {
		t.Fatalf("UpsertChapters() error = %v", err)
	}

	ch1, err := repo.GetChapterByLabel(ctx, title.ID, "1")
	if err != nil {
		t.Fatalf("GetChapterByLabel(1) error = %v", err)
	}
	ch2, err := repo.GetChapterByLabel(ctx, title.ID, "2")
	if err != nil {
		t.Fatalf("GetChapterByLabel(2) error = %v", err)
	}
	if err := repo.MarkDownloadCompleted(ctx, ch1.ID, "chapter-1.cbz", 100, 3); err != nil {
		t.Fatalf("MarkDownloadCompleted(1) error = %v", err)
	}
	if err := repo.MarkDownloadCompleted(ctx, ch2.ID, "chapter-2.cbz", 100, 2); err != nil {
		t.Fatalf("MarkDownloadCompleted(2) error = %v", err)
	}

	status, err := repo.MarkPageRead(ctx, ch1.ID, 1, 3)
	if err != nil {
		t.Fatalf("MarkPageRead() error = %v", err)
	}
	if status.ReadPages != 1 || status.TotalPages != 3 || status.FirstUnreadPage != 2 || status.Completed {
		t.Fatalf("page status = %+v, want 1/3 first unread 2 incomplete", status)
	}
	list, err := repo.ListChapters(ctx, title.ID)
	if err != nil {
		t.Fatalf("ListChapters() error = %v", err)
	}
	if !list[0].Downloaded || list[0].Read || list[0].ReadPages != 1 || list[0].TotalPages != 3 {
		t.Fatalf("chapter list progress = %+v, want partial read state", list[0])
	}

	progress, err := repo.ReaderProgress(ctx, title.ID)
	if err != nil {
		t.Fatalf("ReaderProgress() error = %v", err)
	}
	if progress.TotalChapters != 2 || progress.ReadChapters != 0 || progress.NextChapterID != ch1.ID || progress.NextPage != 2 {
		t.Fatalf("progress = %+v, want next chapter 1 page 2", progress)
	}

	status, err = repo.MarkChapterRead(ctx, ch1.ID)
	if err != nil {
		t.Fatalf("MarkChapterRead() error = %v", err)
	}
	if !status.Completed || status.ReadPages != 3 || status.FirstUnreadPage != 0 {
		t.Fatalf("completed status = %+v, want complete 3 pages", status)
	}

	progress, err = repo.ReaderProgress(ctx, title.ID)
	if err != nil {
		t.Fatalf("ReaderProgress(after complete) error = %v", err)
	}
	if progress.ReadChapters != 1 || progress.NextChapterID != ch2.ID || progress.NextPage != 1 {
		t.Fatalf("progress after complete = %+v, want next chapter 2 page 1", progress)
	}

	status, err = repo.MarkChapterUnread(ctx, ch1.ID)
	if err != nil {
		t.Fatalf("MarkChapterUnread() error = %v", err)
	}
	if status.Completed || status.ReadPages != 0 || status.FirstUnreadPage != 1 {
		t.Fatalf("unread status = %+v, want unread at page 1", status)
	}
	count, err := repo.MarkChapterRangeRead(ctx, title.ID, "1", "2")
	if err != nil {
		t.Fatalf("MarkChapterRangeRead() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("MarkChapterRangeRead() count = %d, want 2", count)
	}
	progress, err = repo.ReaderProgress(ctx, title.ID)
	if err != nil {
		t.Fatalf("ReaderProgress(after range read) error = %v", err)
	}
	if progress.ReadChapters != 2 || progress.NextChapterID != 0 {
		t.Fatalf("progress after range read = %+v, want all read", progress)
	}
	count, err = repo.MarkChapterRangeUnread(ctx, title.ID, "Ch. 1", "Episode 2")
	if err != nil {
		t.Fatalf("MarkChapterRangeUnread() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("MarkChapterRangeUnread() count = %d, want 2", count)
	}
}

func TestUnlinkSourcePrunesUndownloadedChapters(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	repo := NewRepository(db)
	title, err := repo.AddTitle(ctx, AddTitleParams{
		SourceURL: "pending:1", DisplayTitle: "Demo", Monitored: true, RefreshInterval: "12h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sources (id, name) VALUES ('a','A'), ('b','B')`); err != nil {
		t.Fatal(err)
	}
	if err := repo.LinkSource(ctx, title.ID, "https://a.test/manga/demo", "a"); err != nil {
		t.Fatal(err)
	}
	if err := repo.LinkSource(ctx, title.ID, "https://b.test/manga/demo", "b"); err != nil {
		t.Fatal(err)
	}
	// Chapters carrying source A urls: one downloaded, one missing. Plus a
	// local imported chapter that must never be pruned.
	if _, err := repo.UpsertChapters(ctx, title.ID, []chapters.Chapter{
		{Chapter: providers.Chapter{URL: "https://www.a.test/manga/demo/ch-1", Label: "1", NumMain: 1}},
		{Chapter: providers.Chapter{URL: "https://a.test/manga/demo/ch-2", Label: "2", NumMain: 2}},
		{Chapter: providers.Chapter{URL: "local:demo/ch-3.cbz", Label: "3", NumMain: 3}},
	}); err != nil {
		t.Fatal(err)
	}
	ch1, err := repo.GetChapterByLabel(ctx, title.ID, "1")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkDownloadCompleted(ctx, ch1.ID, "/tmp/ch-1.cbz", 10, 5); err != nil {
		t.Fatal(err)
	}

	// Unlink source A (the active one is B; A's undownloaded chapters go).
	if err := repo.UnlinkSource(ctx, title.ID, "https://a.test/manga/demo"); err != nil {
		t.Fatal(err)
	}

	left, err := repo.ListChapters(ctx, title.ID)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, c := range left {
		labels[c.Label] = true
	}
	if !labels["1"] {
		t.Fatal("downloaded chapter 1 was pruned")
	}
	if labels["2"] {
		t.Fatal("undownloaded chapter 2 from unlinked source was kept")
	}
	if !labels["3"] {
		t.Fatal("local imported chapter 3 was pruned")
	}
	links, err := repo.ListTitleSources(ctx, title.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].SourceID != "b" {
		t.Fatalf("links = %#v", links)
	}
}
