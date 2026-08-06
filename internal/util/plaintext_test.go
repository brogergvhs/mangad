package util

import "testing"

func TestPlainSnippet(t *testing.T) {
	for _, tc := range []struct {
		in   string
		max  int
		want string
	}{
		{"<b>Bold</b> tale<br>of &amp; things", 100, "Bold tale of & things"},
		{"one  two\n\nthree", 100, "one two three"},
		{"alpha beta gamma", 10, "alpha…"},
		{"", 10, ""},
	} {
		if got := PlainSnippet(tc.in, tc.max); got != tc.want {
			t.Fatalf("PlainSnippet(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}
