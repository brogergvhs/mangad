package service

import "testing"

func TestParseChapterFile(t *testing.T) {
	cases := []struct {
		name      string
		wantLabel string
		wantNum   int
	}{
		{"Chapter 001.cbz", "1", 1},
		{"c012.cbz", "12", 12},
		{"Ch 12.5.cbz", "12.5", 12},
		{"Frieren - Chapter 023 - x.cbz", "23", 23},
		{"012.05.cbz", "12.05", 12},
		{"Oneshot.cbz", "Oneshot", 0},
		{"vol 2 ch 7.cbz", "7", 7},
	}
	for _, tc := range cases {
		label, num := parseChapterFile(tc.name)
		if label != tc.wantLabel || num != tc.wantNum {
			t.Errorf("parseChapterFile(%q) = %q/%d, want %q/%d", tc.name, label, num, tc.wantLabel, tc.wantNum)
		}
	}
}
