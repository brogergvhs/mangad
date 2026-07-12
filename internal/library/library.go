// Package library stores tracked manga titles and discovered chapters.
package library

import "time"

// DefaultDownloadAttempts is how often a failed chapter download is retried
// before it stops counting as missing; a completed download resets it.
const DefaultDownloadAttempts = 3

// Title is a tracked manga source URL.
type Title struct {
	ID              int64
	CatalogMangaID  *int64
	SourceID        string
	SourceURL       string
	DisplayTitle    string
	CoverImage      string
	OutputPath      string
	Monitored       bool
	RefreshInterval string
	LastRefreshedAt *time.Time
	DiscoveredCount int64
	MissingCount    int64
	FailedCount     int64 // chapters that gave up after the attempt cap
	ReadCount       int64 // chapters fully read
	CompletedCount  int64
	SizeBytes       int64
	Pages           int64
	ReleaseStatus   string
	IsAdult         bool
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

// ChapterReadStatus describes reader progress for one downloaded chapter.
type ChapterReadStatus struct {
	Chapter
	Downloaded      bool
	OutputFile      string
	Bytes           int64
	Pages           int
	ReadPages       int
	TotalPages      int
	LastPage        int
	Completed       bool
	FirstUnreadPage int
	LastReadAt      *time.Time
	CompletedAt     *time.Time
}

// TitleReadProgress describes resume/progress state for a title.
type TitleReadProgress struct {
	Title
	Chapters      []ChapterReadStatus
	ReadChapters  int
	TotalChapters int
	ReadPages     int64
	TotalPages    int64
	NextChapterID int64
	NextPage      int
}
