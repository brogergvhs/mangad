package util

import (
	"html"
	"regexp"
	"strings"
)

var tagPattern = regexp.MustCompile(`<[^>]*>`)

// PlainSnippet strips simple HTML, collapses whitespace, and truncates to
// roughly max runes on a word boundary.
func PlainSnippet(s string, max int) string {
	s = html.UnescapeString(tagPattern.ReplaceAllString(s, " "))
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	cut := string(runes[:max])
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}
