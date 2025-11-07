package chapters

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/brogergvhs/mangad/internal/providers"
)

type Chapter struct {
	providers.Chapter
}

func sanitize(s string) string {
	s = strings.ToLower(s)

	repl := []string{
		"•", "_",
		"-", "_",
		"—", "_",
		"–", "_",
		"/", "_",
		"\\", "_",
		".", "_",
		" ", "_",
		"(", "",
		")", "",
	}
	for i := 0; i < len(repl); i += 2 {
		s = strings.ReplaceAll(s, repl[i], repl[i+1])
	}

	clean := make([]rune, 0, len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			clean = append(clean, r)
		}
	}
	s = string(clean)

	reUnderscore := regexp.MustCompile(`_+`)
	s = reUnderscore.ReplaceAllString(s, "_")

	return strings.Trim(s, "_")
}

func (c Chapter) baseName() string {
	lbl := sanitize(c.Label)

	title := sanitize(c.Title)

	if title != "" && title != lbl {
		return lbl + "_" + title
	}
	return lbl
}

func (c Chapter) FolderName() string {
	return c.baseName() + "_tmp"
}

func (c Chapter) OutputCBZ() string {
	return c.baseName() + ".cbz"
}

func (c Chapter) OutputCBZPath(out string) string {
	return filepath.Join(out, c.OutputCBZ())
}
