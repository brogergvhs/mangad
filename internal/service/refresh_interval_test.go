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
