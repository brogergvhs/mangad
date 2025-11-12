package chapters

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrInvalidRange = errors.New("invalid chapter range")
	ErrInvalidList  = errors.New("invalid chapter list")
	ErrNotFound     = errors.New("chapter not found")
)

func Filter(
	all []Chapter,
	chapter string,
	rng string,
	excludeRng string,
	list string,
	excludeList string,
) ([]Chapter, error) {
	if chapter != "" {
		return filterSingle(all, chapter)
	}

	keep := make([]bool, len(all))
	for i := range keep {
		keep[i] = true
	}

	if err := applyRanges(keep, rng, excludeRng, len(all)); err != nil {
		return nil, err
	}
	if err := applyLists(keep, list, excludeList, len(all)); err != nil {
		return nil, err
	}

	return buildResult(all, keep), nil
}

func filterSingle(all []Chapter, chapter string) ([]Chapter, error) {
	if byLabel := FilterChaptersByLabel(all, chapter); len(byLabel) > 0 {
		return byLabel, nil
	}

	n := len(all)
	if idx, err := strconv.Atoi(strings.TrimSpace(chapter)); err == nil {
		if idx <= 0 || idx > n {
			return nil, fmt.Errorf("%w: chapter index %d out of range", ErrNotFound, idx)
		}
		return []Chapter{all[idx-1]}, nil
	}

	return nil, fmt.Errorf("%w: %q", ErrNotFound, chapter)
}

func applyRanges(keep []bool, rng, excludeRng string, n int) error {
	if rng != "" {
		start, end, err := parseRange(rng, n)
		if err != nil {
			return err
		}
		applyIncludeRange(keep, start, end)
	}

	if excludeRng != "" {
		start, end, err := parseRange(excludeRng, n)
		if err != nil {
			return err
		}
		applyExcludeRange(keep, start, end)
	}

	return nil
}

func applyLists(keep []bool, list, excludeList string, n int) error {
	if list != "" {
		indices, err := parseList(list, n)
		if err != nil {
			return err
		}
		applyIncludeList(keep, indices)
	}

	if excludeList != "" {
		indices, err := parseList(excludeList, n)
		if err != nil {
			return err
		}
		applyExcludeList(keep, indices)
	}

	return nil
}

func buildResult(all []Chapter, keep []bool) []Chapter {
	out := make([]Chapter, 0, len(all))
	for i, ok := range keep {
		if ok {
			out = append(out, all[i])
		}
	}

	return out
}

func FilterChaptersByLabel(all []Chapter, label string) []Chapter {
	out := make([]Chapter, 0, 4)
	for _, ch := range all {
		if ch.Label == label {
			out = append(out, ch)
		}
	}

	return out
}

func parseRange(s string, max int) (start, end int, err error) {
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: %q", ErrInvalidRange, s)
	}

	start, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	end, e2 := strconv.Atoi(strings.TrimSpace(parts[1]))

	if e1 != nil || e2 != nil {
		return 0, 0, fmt.Errorf("%w: non-integer values in %q", ErrInvalidRange, s)
	}
	if start <= 0 || end <= 0 || start > end || end > max {
		return 0, 0, fmt.Errorf("%w: out of bounds %q", ErrInvalidRange, s)
	}

	return start - 1, end - 1, nil
}

func applyIncludeRange(keep []bool, start, end int) {
	for i := range keep {
		keep[i] = i >= start && i <= end
	}
}

func applyExcludeRange(keep []bool, start, end int) {
	for i := start; i <= end; i++ {
		keep[i] = false
	}
}

func parseList(s string, max int) ([]int, error) {
	if s == "" {
		return nil, nil
	}

	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > max {
			return nil, fmt.Errorf("%w: %q", ErrInvalidList, p)
		}
		out = append(out, n-1)
	}
	return out, nil
}

func applyIncludeList(keep []bool, indices []int) {
	allowed := make([]bool, len(keep))
	for _, i := range indices {
		allowed[i] = true
	}
	for i := range keep {
		keep[i] = keep[i] && allowed[i]
	}
}

func applyExcludeList(keep []bool, indices []int) {
	for _, i := range indices {
		keep[i] = false
	}
}
