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
		// leading chapter number "N: Title; Part M" must win over the trailing part
		{"1: The Boy And The Girl; Part 1.cbz", "1", 1},
		{"11: The Girl; Part 1.cbz", "11", 11},
		{"10: The Girl And The Boy; Part 2.cbz", "10", 10},
		{"21.cbz", "21", 21},
		{"5 - The Strange Man.cbz", "5", 5},
		{"100 Bullets 5.cbz", "5", 5}, // title starting with a number, not a leading chapter
	}
	for _, tc := range cases {
		label, num := parseChapterFile(tc.name)
		if label != tc.wantLabel || num != tc.wantNum {
			t.Errorf("parseChapterFile(%q) = %q/%d, want %q/%d", tc.name, label, num, tc.wantLabel, tc.wantNum)
		}
	}
}
