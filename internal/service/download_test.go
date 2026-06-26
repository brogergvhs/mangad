package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/providers"
)

func TestFetchChapters(t *testing.T) {
	t.Parallel()

	svc := NewDownloadService(
		config.DefaultConfig(),
		&http.Client{},
		fakeScraper{
			chapters: []providers.Chapter{
				{URL: "https://example.test/chapter-1", Title: "Chapter 1", Label: "1"},
				{URL: "https://example.test/chapter-2", Title: "Chapter 2", Label: "2"},
			},
		},
		nil,
		nil,
	)

	got, err := svc.FetchChapters(context.Background(), "https://example.test")
	if err != nil {
		t.Fatalf("FetchChapters() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("FetchChapters() len = %d, want 2", len(got))
	}
	if got[0].Label != "1" || got[1].URL != "https://example.test/chapter-2" {
		t.Fatalf("FetchChapters() = %+v", got)
	}
}

func TestSelectChapters(t *testing.T) {
	t.Parallel()

	svc := NewDownloadService(config.DefaultConfig(), &http.Client{}, fakeScraper{}, nil, nil)
	all := mustFetchChapters(t, svc)

	tests := []struct {
		name      string
		selection ChapterSelection
		want      []string
		wantErr   bool
	}{
		{
			name: "all chapters",
			want: []string{"1", "2", "3", "4"},
		},
		{
			name:      "single label",
			selection: ChapterSelection{Chapter: "2"},
			want:      []string{"2"},
		},
		{
			name:      "range with exclude list",
			selection: ChapterSelection{Range: "1-4", ExcludeList: "2,4"},
			want:      []string{"1", "3"},
		},
		{
			name:      "explicit list",
			selection: ChapterSelection{List: "1,3"},
			want:      []string{"1", "3"},
		},
		{
			name:      "invalid range",
			selection: ChapterSelection{Range: "10-20"},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := svc.SelectChapters(all, tt.selection)
			if tt.wantErr {
				if err == nil {
					t.Fatal("SelectChapters() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("SelectChapters() error = %v", err)
			}

			gotLabels := make([]string, 0, len(got))
			for _, ch := range got {
				gotLabels = append(gotLabels, ch.Label)
			}
			if !equalStrings(gotLabels, tt.want) {
				t.Fatalf("SelectChapters() labels = %v, want %v", gotLabels, tt.want)
			}
		})
	}
}

func mustFetchChapters(t *testing.T, _ *DownloadService) []chapters.Chapter {
	t.Helper()

	raw := []providers.Chapter{
		{URL: "https://example.test/chapter-1", Title: "Chapter 1", Label: "1"},
		{URL: "https://example.test/chapter-2", Title: "Chapter 2", Label: "2"},
		{URL: "https://example.test/chapter-3", Title: "Chapter 3", Label: "3"},
		{URL: "https://example.test/chapter-4", Title: "Chapter 4", Label: "4"},
	}
	out := make([]chapters.Chapter, len(raw))
	for i, ch := range raw {
		out[i] = chapters.Chapter{Chapter: ch}
	}

	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

type fakeScraper struct {
	chapters []providers.Chapter
	images   []string
}

func (s fakeScraper) GetChapters(_ context.Context, _ string) ([]providers.Chapter, error) {
	return s.chapters, nil
}

func (s fakeScraper) GetImages(_ context.Context, _ string) ([]string, error) {
	return s.images, nil
}
