package database

import (
	"fmt"
	"strings"
	"time"
)

// Scanner abstracts *sql.Row and *sql.Rows for shared scan helpers.
type Scanner interface {
	Scan(dest ...any) error
}

// ParseTime parses the timestamp formats SQLite stores; empty means zero.
func ParseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("parse sqlite time %q", value)
}

// FormatTime renders a timestamp the way CURRENT_TIMESTAMP stores it, so
// lexicographic comparisons in SQL stay correct.
func FormatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05")
}

// BoolToInt converts a bool to SQLite's 0/1.
func BoolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
