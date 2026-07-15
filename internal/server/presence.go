// In-memory reader presence for the session viewer
// keyed by session so each device shows up separately.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type sessionKeyCtx struct{}

func withSessionKey(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	sum := sha256.Sum256([]byte(token))
	return context.WithValue(ctx, sessionKeyCtx{}, hex.EncodeToString(sum[:]))
}

func sessionKey(ctx context.Context) string {
	key, _ := ctx.Value(sessionKeyCtx{}).(string)
	return key
}

type readingActivity struct {
	UserID       int64
	TitleID      int64
	Title        string
	ChapterLabel string
	Page         int
	Total        int
	UpdatedAt    time.Time
}

type presenceTracker struct {
	mu      sync.Mutex
	reading map[string]readingActivity // session key -> latest reader activity
}

var presence = presenceTracker{reading: map[string]readingActivity{}}

// SetTitle records that the session opened a title in the reader.
func (p *presenceTracker) SetTitle(ctx context.Context, userID, titleID int64, title string) {
	key := sessionKey(ctx)
	if key == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reading[key] = readingActivity{UserID: userID, TitleID: titleID, Title: title, UpdatedAt: time.Now()}
}

// SetPage updates the session's reading position, keeping the title info.
func (p *presenceTracker) SetPage(ctx context.Context, userID int64, chapterLabel string, page, total int) {
	key := sessionKey(ctx)
	if key == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.reading[key]
	a.UserID = userID
	a.ChapterLabel = chapterLabel
	a.Page = page
	a.Total = total
	a.UpdatedAt = time.Now()
	p.reading[key] = a
}

// Get returns the session's activity when fresher than maxAge.
func (p *presenceTracker) Get(key string, maxAge time.Duration) (readingActivity, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	a, ok := p.reading[key]
	if !ok {
		return readingActivity{}, false
	}
	if time.Since(a.UpdatedAt) > maxAge {
		delete(p.reading, key)
		return readingActivity{}, false
	}
	return a, true
}
