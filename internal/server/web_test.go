package server

import (
	"testing"

	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
)

func TestTitleActivityFromTargetedTitleJob(t *testing.T) {
	t.Parallel()

	title := library.Title{ID: 42, Monitored: true}
	running, label, failed, _ := titleActivityFrom([]jobs.Job{{
		Type:    jobs.TypeDownloadMissing,
		Status:  "queued",
		Payload: `{"title_id":42}`,
	}}, title)
	if failed || label != "downloading" || !running[jobs.TypeDownloadMissing] {
		t.Fatalf("activity = running %v label %q failed %v", running, label, failed)
	}
}

func TestTitleActivityFromIgnoresGlobalTitleJob(t *testing.T) {
	t.Parallel()

	running, label, failed, _ := titleActivityFrom([]jobs.Job{{
		Type:    jobs.TypeDownloadMissing,
		Status:  "queued",
		Payload: `{}`,
	}}, library.Title{ID: 42, Monitored: true})
	if len(running) != 0 || label != "" || failed {
		t.Fatalf("activity = running %v label %q failed %v", running, label, failed)
	}
}
