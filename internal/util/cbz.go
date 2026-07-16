package util

import (
	"archive/zip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// CBZImageEntries returns a CBZ's image entries in natural page order.
func CBZImageEntries(files []*zip.File) []*zip.File {
	out := make([]*zip.File, 0, len(files))
	for _, file := range files {
		if file.FileInfo().IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(file.Name)) {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".avif":
			out = append(out, file)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return NaturalLess(out[i].Name, out[j].Name)
	})
	return out
}

// NaturalLess compares strings with embedded numbers numerically, so
// "2.jpg" < "10.jpg".
func NaturalLess(a, b string) bool {
	as, bs := []rune(strings.ToLower(filepath.Base(a))), []rune(strings.ToLower(filepath.Base(b)))
	for len(as) > 0 && len(bs) > 0 {
		ac, ar := nextNaturalChunk(as)
		bc, br := nextNaturalChunk(bs)
		if unicode.IsDigit(ac[0]) && unicode.IsDigit(bc[0]) {
			ai, _ := strconv.Atoi(string(ac))
			bi, _ := strconv.Atoi(string(bc))
			if ai != bi {
				return ai < bi
			}
		} else if string(ac) != string(bc) {
			return string(ac) < string(bc)
		}
		as, bs = ar, br
	}
	return len(as) < len(bs)
}

func nextNaturalChunk(s []rune) ([]rune, []rune) {
	digit := unicode.IsDigit(s[0])
	i := 1
	for i < len(s) && unicode.IsDigit(s[i]) == digit {
		i++
	}
	return s[:i], s[i:]
}
