package service

import (
	"testing"
	"time"

	"github.com/brogergvhs/mangad/internal/library"
)

func TestTitleRefreshDue(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { x := now.Add(-d); return &x }
	cases := []struct {
		name     string
		interval string
		last     *time.Time
		want     bool
	}{
		{"empty follows global", "", ago(time.Minute), true},
		{"never refreshed", "6h", nil, true},
		{"not yet due", "6h", ago(2 * time.Hour), false},
		{"due", "6h", ago(7 * time.Hour), true},
		{"invalid falls through", "nonsense", ago(time.Minute), true},
	}
	for _, tc := range cases {
		got := titleRefreshDue(library.Title{RefreshInterval: tc.interval, LastRefreshedAt: tc.last}, now)
		if got != tc.want {
			t.Errorf("%s: due=%v want %v", tc.name, got, tc.want)
		}
	}
}

func TestGlobalJobsSkipUnlinkedTitles(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		url  string
		want bool
	}{
		{"https://mangapill.com/manga/1/x", true},
		{"local:Infinite%20leveling%20murim", false},
		{"pending:42", false},
	} {
		title := library.Title{SourceURL: tc.url, Monitored: true, MissingCount: 3, DiscoveredCount: 10}
		if got := globalTitleJobApplies("refresh_title", title, now); got != tc.want {
			t.Errorf("refresh applies(%q) = %v, want %v", tc.url, got, tc.want)
		}
		if got := globalTitleJobApplies("download_missing", title, now); got != tc.want {
			t.Errorf("download applies(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}
