package ui

import "sync/atomic"

type Stats struct {
	TotalImages   atomic.Int64
	TotalBytes    atomic.Int64
	TotalChapters atomic.Int64
}
