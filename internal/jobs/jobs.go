// Package jobs stores background jobs.
package jobs

import "time"

const (
	TypeRefreshTitle    = "refresh_title"
	TypeScanDownloads   = "scan_downloads"
	TypeDownloadMissing = "download_missing"
	TypeVerifySource    = "verify_source"
	TypeMatchSources    = "match_sources"
)

// Job is a persisted background job.
type Job struct {
	ID        int64
	Type      string
	Status    string
	Payload   string
	RunAfter  time.Time
	Attempts  int
	LastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}
