package providers

import (
	"strconv"
	"strings"
)

func Filter(all []Chapter, chapter, rng, list string) []Chapter {
	if chapter != "" {
		byLabel := FilterByLabel(all, chapter)
		if len(byLabel) > 0 {
			return byLabel
		}

		if idx, err := strconv.Atoi(chapter); err == nil {
			if idx > 0 && idx <= len(all) {
				return []Chapter{all[idx-1]}
			}
		}

		return nil
	}

	if rng != "" {
		return FilterRange(all, rng)
	}
	if list != "" {
		return FilterList(all, list)
	}

	return all
}

func FilterByLabel(all []Chapter, label string) []Chapter {
	out := []Chapter{}
	for _, c := range all {
		if c.Label == label {
			out = append(out, c)
		}
	}

	return out
}

func FilterRange(all []Chapter, rng string) []Chapter {
	parts := strings.Split(rng, "-")
	if len(parts) != 2 {
		return nil
	}

	start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))

	if err1 != nil || err2 != nil {
		return nil
	}
	if start <= 0 || end <= 0 || start > end || end > len(all) {
		return nil
	}

	return all[start-1 : end]
}

func FilterList(all []Chapter, list string) []Chapter {
	var out []Chapter
	parts := strings.SplitSeq(list, ",")

	for p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		idx, err := strconv.Atoi(p)
		if err != nil || idx <= 0 || idx > len(all) {
			continue
		}

		out = append(out, all[idx-1])
	}

	return out
}
