package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/providers"
)

func TestConfigForTitleOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   *config.Config
		title library.Title
		want  string
	}{
		{
			name: "explicit title output wins",
			cfg:  &config.Config{Output: "Solo_leveling"},
			title: library.Title{
				ID:           2,
				DisplayTitle: "Gachiakuta",
				OutputPath:   "custom/Gachiakuta",
			},
			want: "custom/Gachiakuta",
		},
		{
			name: "default output is title directory",
			cfg:  &config.Config{Output: "Solo_leveling"},
			title: library.Title{
				ID:           2,
				DisplayTitle: "Gachiakuta",
			},
			want: "Gachiakuta",
		},
		{
			name: "display title is path safe",
			cfg:  &config.Config{Output: "."},
			title: library.Title{
				ID:           3,
				DisplayTitle: "Solo Leveling!",
			},
			want: "Solo_Leveling",
		},
		{
			name: "empty title fallback",
			cfg:  &config.Config{Output: "."},
			title: library.Title{
				ID: 4,
			},
			want: "title_4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := configForTitle(tt.cfg, tt.title)
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
	if err := repo.MarkDownloadCompleted(ctx, chapter.ID, out, 3); err != nil {
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
