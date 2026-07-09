package service

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// stallGuard cancels a job context only when no progress is reported for a
// duration. Jobs that keep making progress (e.g. a 1200-chapter download)
// run as long as they need; only genuinely stuck jobs are aborted.
type stallGuard struct {
	mu    sync.Mutex
	timer *time.Timer
	d     time.Duration
}

// stallContext derives a context that is cancelled after d of inactivity.
// The returned guard doubles as a ProgressManager: every progress event
// resets the inactivity timer. Jobs that never report progress behave as if
// under a plain wall-clock timeout.
func stallContext(parent context.Context, d time.Duration) (context.Context, *stallGuard, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	g := &stallGuard{d: d}
	g.timer = time.AfterFunc(d, func() {
		cancel(fmt.Errorf("job made no progress for %s", d))
	})
	stop := func() {
		g.mu.Lock()
		g.timer.Stop()
		g.mu.Unlock()
		cancel(nil)
	}
	return ctx, g, stop
}

// Touch resets the inactivity timer.
func (g *stallGuard) Touch() {
	g.mu.Lock()
	g.timer.Reset(g.d)
	g.mu.Unlock()
}

// Register implements ProgressManager.
func (g *stallGuard) Register(string) ProgressHandle { return stallHandle{g} }

// Close implements ProgressManager.
func (g *stallGuard) Close() {}

type stallHandle struct{ g *stallGuard }

func (h stallHandle) SetTotal(int)           { h.g.Touch() }
func (h stallHandle) Update(int, int, int64) { h.g.Touch() }
func (h stallHandle) MarkDone()              { h.g.Touch() }
