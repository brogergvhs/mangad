package server

import (
	"testing"

	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
)

func TestTitleActivityFromGlobalTitleJob(t *testing.T) {
	t.Parallel()

	title := library.Title{ID: 42, Monitored: true}
	running, label, failed, _ := titleActivityFrom([]jobs.Job{{
		Type:    jobs.TypeDownloadMissing,
		Status:  "queued",
		Payload: `{}`,
	}}, title)
	if failed || label != "downloading" || !running[jobs.TypeDownloadMissing] {
		t.Fatalf("activity = running %v label %q failed %v", running, label, failed)
	}
}

func TestTitleActivityFromGlobalDownloadSkipsUnmonitored(t *testing.T) {
	t.Parallel()

	running, label, failed, _ := titleActivityFrom([]jobs.Job{{
		Type:    jobs.TypeDownloadMissing,
		Status:  "queued",
		Payload: `{}`,
	}}, library.Title{ID: 42, Monitored: false})
	if len(running) != 0 || label != "" || failed {
		t.Fatalf("activity = running %v label %q failed %v", running, label, failed)
	}
}
