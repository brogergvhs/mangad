package chapters

import (
	"strconv"
	"strings"
)

func Filter(all []Chapter, chapter string, rng string, list string) []Chapter {
	if chapter != "" {
		byLabel := FilterChaptersByLabel(all, chapter)
		if len(byLabel) > 0 {
			return byLabel
		}
		if idx, err := strconv.Atoi(chapter); err == nil {
			if idx > 0 && idx <= len(all) {
				return []Chapter{all[idx-1]}
			}
		}
		return []Chapter{}
	}
	if rng != "" {
		return FilterChapterRange(all, rng)
	}
	if list != "" {
		return FilterChapterList(all, list)
	}
	return all
}

func FilterChaptersByLabel(all []Chapter, label string) []Chapter {
	var out []Chapter
	for _, ch := range all {
		if ch.Label == label {
			out = append(out, ch)
		}
	}
	return out
}

func FilterChapterRange(all []Chapter, rng string) []Chapter {
	parts := strings.Split(rng, "-")
	if len(parts) != 2 {
		return nil
	}
	start, err1 := atoi(parts[0])
	end, err2 := atoi(parts[1])
	if err1 != nil || err2 != nil {
		return nil
	}
	if start <= 0 || end <= 0 || start > end || end > len(all) {
		return nil
	}
	return all[start-1 : end]
}

func FilterChapterList(all []Chapter, list string) []Chapter {
	nums := strings.Split(list, ",")
	out := []Chapter{}
	for _, n := range nums {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		idx, err := atoi(n)
		if err != nil {
			continue
		}
		if idx > 0 && idx <= len(all) {
			out = append(out, all[idx-1])
		}
	}
	return out
}

func atoi(s string) (int, error) {
	return strconv.Atoi(strings.TrimSpace(s))
}
