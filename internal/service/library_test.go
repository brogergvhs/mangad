package service

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/providers"
	"github.com/brogergvhs/mangad/internal/ui"
)

func TestLibraryAddTitleResolvesSourceID(t *testing.T) {
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

	svc := newLibraryService(db)
	title, err := svc.AddTitle(ctx, library.AddTitleParams{
		SourceURL:    "https://mangakatana.com/manga/the-great-mage-returns-after-4000-years.24531",
		DisplayTitle: "The Great Mage Returns After 4000 Years",
		Monitored:    true,
	})
	if err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}
	if title.SourceID != "mangakatana" {
		t.Fatalf("SourceID = %q, want mangakatana", title.SourceID)
	}
}

func TestLibraryDownloadServiceUsesTitleSource(t *testing.T) {
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

	svc := newLibraryService(db)
	downloadSvc, err := svc.downloadServiceForTitle(ctx, &config.Config{}, ui.NewLogger(false), nil, library.Title{
		SourceID:  "comickz",
		SourceURL: "https://comickz.co.uk/comic/02-the-great-mage-returns-after-4000-years",
	})
	if err != nil {
		t.Fatalf("downloadServiceForTitle() error = %v", err)
	}
	if !downloadSvc.cfg.BrowserDownload.Enabled {
		t.Fatal("BrowserDownload.Enabled = false, want source profile enabled")
	}
}

func TestConfigForTitleOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		cfg   *config.Config
		title library.Title
		want  string
	}{
		{
			name: "explicit title output wins",
			cfg:  &config.Config{Output: "Solo_leveling", DownloadDir: root},
			title: library.Title{
				ID:           2,
				DisplayTitle: "Gachiakuta",
				OutputPath:   "custom/Gachiakuta",
			},
			want: filepath.Join(root, "custom/Gachiakuta"),
		},
		{
			name: "default output is title directory",
			cfg:  &config.Config{Output: "Solo_leveling", DownloadDir: "."},
			title: library.Title{
				ID:           2,
				DisplayTitle: "Gachiakuta",
			},
			want: filepath.Join(cwd, "Gachiakuta"),
		},
		{
			name: "download dir prefixes title directory",
			cfg:  &config.Config{Output: "Solo_leveling", DownloadDir: root},
			title: library.Title{
				ID:           2,
				DisplayTitle: "Gachiakuta",
			},
			want: filepath.Join(root, "Gachiakuta"),
		},
		{
			name: "display title is path safe",
			cfg:  &config.Config{Output: ".", DownloadDir: "."},
			title: library.Title{
				ID:           3,
				DisplayTitle: "Solo Leveling!",
			},
			want: filepath.Join(cwd, "Solo_Leveling"),
		},
		{
			name: "empty title fallback",
			cfg:  &config.Config{Output: ".", DownloadDir: "."},
			title: library.Title{
				ID: 4,
			},
			want: filepath.Join(cwd, "title_4"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := configForTitle(tt.cfg, tt.title)
			if err != nil {
				t.Fatal(err)
			}
			if got.Output != tt.want {
				t.Fatalf("configForTitle().Output = %q, want %q", got.Output, tt.want)
			}
			if tt.cfg.Output == "" {
				return
			}
			if tt.cfg.Output != "." && tt.title.OutputPath == "" && got.Output == tt.cfg.Output {
				t.Fatalf("configForTitle reused active config output %q", tt.cfg.Output)
			}
		})
	}
}

func TestConfigForTitleRejectsOutputOutsideDownloadRoot(t *testing.T) {
	t.Parallel()

	_, err := configForTitle(&config.Config{DownloadDir: t.TempDir()}, library.Title{OutputPath: "../outside"})
	if err == nil {
		t.Fatal("configForTitle() error = nil")
	}
}

func TestScanDownloadsMarksMissingFilesFailed(t *testing.T) {
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

	repo := library.NewRepository(db)
	title, err := repo.AddTitle(ctx, library.AddTitleParams{
		SourceURL:    "https://example.test/manga",
		DisplayTitle: "Example",
		Monitored:    true,
	})
	if err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}
	if _, err := repo.UpsertChapters(ctx, title.ID, []chapters.Chapter{
		{Chapter: providers.Chapter{URL: "https://example.test/ch-1", Title: "Chapter 1", Label: "1", NumMain: 1}},
	}); err != nil {
		t.Fatalf("UpsertChapters() error = %v", err)
	}
	chapter, err := repo.GetChapterByLabel(ctx, title.ID, "1")
	if err != nil {
		t.Fatalf("GetChapterByLabel() error = %v", err)
	}

	out := filepath.Join(t.TempDir(), "chapter.cbz")
	if err := os.WriteFile(out, []byte("cbz"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := repo.MarkDownloadCompleted(ctx, chapter.ID, out, 3, 2); err != nil {
		t.Fatalf("MarkDownloadCompleted() error = %v", err)
	}

	svc := &LibraryService{repo: repo}
	result, err := svc.ScanDownloads(ctx, title.ID)
	if err != nil {
		t.Fatalf("ScanDownloads() existing file error = %v", err)
	}
	if result.Checked != 1 || result.Missing != 0 {
		t.Fatalf("ScanDownloads() existing file = %+v, want checked 1 missing 0", result)
	}

	if err := os.Remove(out); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	result, err = svc.ScanDownloads(ctx, title.ID)
	if err != nil {
		t.Fatalf("ScanDownloads() missing file error = %v", err)
	}
	if result.Checked != 1 || result.Missing != 1 {
		t.Fatalf("ScanDownloads() missing file = %+v, want checked 1 missing 1", result)
	}

	missing, err := repo.ListMissingChapters(ctx, title.ID)
	if err != nil {
		t.Fatalf("ListMissingChapters() error = %v", err)
	}
	if len(missing) != 1 || missing[0].Label != "1" {
		t.Fatalf("ListMissingChapters() = %+v, want chapter 1 missing", missing)
	}
}

func TestOpenLibraryBackfillsDownloadStats(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mangad.db")
	db, err := database.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo := library.NewRepository(db)
	title, err := repo.AddTitle(ctx, library.AddTitleParams{
		SourceURL:    "https://example.test/manga",
		DisplayTitle: "Example",
		Monitored:    true,
	})
	if err != nil {
		t.Fatalf("AddTitle() error = %v", err)
	}
	if _, err := repo.UpsertChapters(ctx, title.ID, []chapters.Chapter{
		{Chapter: providers.Chapter{URL: "https://example.test/ch-1", Title: "Chapter 1", Label: "1", NumMain: 1}},
	}); err != nil {
		t.Fatalf("UpsertChapters() error = %v", err)
	}
	chapter, err := repo.GetChapterByLabel(ctx, title.ID, "1")
	if err != nil {
		t.Fatalf("GetChapterByLabel() error = %v", err)
	}
	cbz := filepath.Join(dir, "chapter.cbz")
	size := writeTestCBZ(t, cbz)
	if err := repo.MarkDownloadCompleted(ctx, chapter.ID, cbz, 0, 0); err != nil {
		t.Fatalf("MarkDownloadCompleted() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	svc, closeDB, err := OpenLibrary(ctx, dbPath)
	if err != nil {
		t.Fatalf("OpenLibrary() error = %v", err)
	}
	defer closeDB()
	gotTitle, err := svc.GetTitle(ctx, title.ID)
	if err != nil {
		t.Fatalf("GetTitle() error = %v", err)
	}
	if gotTitle.SizeBytes != size || gotTitle.Pages != 2 {
		t.Fatalf("stats = %d bytes/%d pages, want %d/2", gotTitle.SizeBytes, gotTitle.Pages, size)
	}
	chs, err := svc.ListChapters(ctx, title.ID)
	if err != nil {
		t.Fatalf("ListChapters() error = %v", err)
	}
	if len(chs) != 1 || chs[0].Bytes != size || chs[0].Pages != 2 {
		t.Fatalf("chapter stats = %+v, want %d bytes/2 pages", chs, size)
	}
}

func writeTestCBZ(t *testing.T, path string) int64 {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{"001.jpg", "002.png"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip Create() error = %v", err)
		}
		if _, err := w.Write([]byte("image")); err != nil {
			t.Fatalf("zip Write() error = %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close() error = %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("file Close() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	return info.Size()
}
