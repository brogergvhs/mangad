// Package library stores tracked manga titles and discovered chapters.
package library

import "time"

// Title is a tracked manga source URL.
type Title struct {
	ID              int64
	CatalogMangaID  *int64
	SourceID        string
	SourceURL       string
	DisplayTitle    string
	OutputPath      string
	Monitored       bool
	RefreshInterval string
	LastRefreshedAt *time.Time
	DiscoveredCount int64
	MissingCount    int64
	CompletedCount  int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// AddTitleParams describes a title to track.
type AddTitleParams struct {
	CatalogMangaID  *int64
	SourceID        string
	SourceURL       string
	DisplayTitle    string
	OutputPath      string
	Monitored       bool
	RefreshInterval string
}

// Chapter is a discovered chapter for a tracked title.
type Chapter struct {
	ID           int64
	TitleID      int64
	Label        string
	Title        string
	URL          string
	NumberMain   int
	SuffixType   string
	SuffixNum    int
	DiscoveredAt time.Time
	UpdatedAt    time.Time
}
