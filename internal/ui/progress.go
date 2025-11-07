package ui

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/brogergvhs/mangad/internal/util"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

type MPBProgressManager struct {
	p *mpb.Progress
}

func NewProgressManager(_ int) *MPBProgressManager {
	p := mpb.New(
		mpb.WithWidth(52),
		mpb.WithOutput(os.Stdout),
		mpb.WithRefreshRate(120*time.Millisecond),
	)
	return &MPBProgressManager{p: p}
}

func (pm *MPBProgressManager) Close() {
	pm.p.Wait()
}

func (pm *MPBProgressManager) Register(prefix string) *ProgressHandle {
	h := &ProgressHandle{
		pm:     pm,
		prefix: prefix,
	}
	h.initBar()
	return h
}

type ProgressHandle struct {
	pm     *MPBProgressManager
	prefix string
	bar    *mpb.Bar

	total int64
	bytes int64

	start   time.Time
	elapsed atomic.Int64

	final atomic.Bool
}

func (h *ProgressHandle) initBar() {
	h.start = time.Now()

	h.bar = h.pm.p.New(
		0,
		mpb.BarStyle().Rbound("]"),

		mpb.PrependDecorators(
			decor.Name(h.prefix+"  "),
		),

		mpb.AppendDecorators(
			decor.Percentage(decor.WCSyncWidth),
			decor.CountersNoUnit(" | %d/%d pages", decor.WCSyncWidth),
			decor.Any(func(_ decor.Statistics) string {
				return " | " + util.Human(atomic.LoadInt64(&h.bytes))
			}),

			decor.Any(func(_ decor.Statistics) string {
				if h.final.Load() {
					sec := h.elapsed.Load()
					return fmt.Sprintf(" | %ds", sec)
				}
				sec := time.Since(h.start).Seconds()

				return fmt.Sprintf(" | %ds", int(sec))
			}),
		),
	)
}

func (h *ProgressHandle) SetTotal(total int) {
	if h.final.Load() {
		return
	}

	atomic.StoreInt64(&h.total, int64(total))
	h.bar.SetTotal(int64(total), false)
}

func (h *ProgressHandle) Update(done, total int, bytes int64) {
	if h.final.Load() {
		return
	}

	if total > 0 {
		atomic.StoreInt64(&h.total, int64(total))
		h.bar.SetTotal(int64(total), false)
	}

	atomic.StoreInt64(&h.bytes, bytes)
	h.bar.SetCurrent(int64(done))
}

func (h *ProgressHandle) MarkDone() {
	if h.final.Swap(true) {
		return
	}

	elapsedSec := int64(time.Since(h.start).Seconds())

	h.elapsed.Store(elapsedSec)
	h.bar.SetCurrent(h.total)
	h.bar.SetTotal(h.total, true)
}
