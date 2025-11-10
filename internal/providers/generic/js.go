package generic

import (
	"regexp"
	"strings"
)

var (
	reJSVar  = regexp.MustCompile(`(?m)(?:var|let|const)\s+([A-Za-z0-9_]+)\s*=\s*["']?([\w\-\/\.]+)["']?;`)
	reJSURL  = regexp.MustCompile(`["'](\/[A-Za-z0-9\/\-\._]+)["']`)
	reJSCall = regexp.MustCompile(`(?:fetch|axios|post|get)\s*\(\s*["']([^"']+)["']`)
)

func looksLikeHTML(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return false
	}

	return strings.Contains(s, "<img") ||
		strings.Contains(s, "<a") ||
		strings.Contains(s, "<div") ||
		strings.Contains(s, "<picture") ||
		strings.Contains(s, "<source")
}

type JSAnalysis struct {
	Vars  map[string]string
	URLs  []string
	Calls []string
}

func ExtractJS(js string) JSAnalysis {
	out := JSAnalysis{
		Vars:  map[string]string{},
		URLs:  []string{},
		Calls: []string{},
	}

	for _, m := range reJSVar.FindAllStringSubmatch(js, -1) {
		out.Vars[m[1]] = m[2]
	}

	for _, m := range reJSURL.FindAllStringSubmatch(js, -1) {
		out.URLs = append(out.URLs, m[1])
	}

	for _, m := range reJSCall.FindAllStringSubmatch(js, -1) {
		out.Calls = append(out.Calls, m[1])
	}

	return out
}
