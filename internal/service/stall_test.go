package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStallContextCancelsWithoutProgress(t *testing.T) {
	t.Parallel()
	ctx, _, stop := stallContext(context.Background(), 50*time.Millisecond)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("stalled context was never cancelled")
	}
	if cause := context.Cause(ctx); cause == nil || !strings.Contains(cause.Error(), "no progress") {
		t.Fatalf("cause = %v", cause)
	}
}

func TestStallContextSurvivesWithProgress(t *testing.T) {
	t.Parallel()
	ctx, guard, stop := stallContext(context.Background(), 60*time.Millisecond)
	defer stop()
	h := guard.Register("ch")
	deadline := time.Now().Add(300 * time.Millisecond) // 5x the stall window
	for time.Now().Before(deadline) {
		h.Update(1, 2, 3)
		time.Sleep(10 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatal("progressing context was cancelled")
		}
	}
}
