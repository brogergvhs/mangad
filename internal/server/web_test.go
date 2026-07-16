package server

import (
	"testing"

	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
)

func TestTitleActivityFromTargetedTitleJob(t *testing.T) {
	t.Parallel()

	title := library.Title{ID: 42, Monitored: true}
	// A queued job locks the button, shows no live label, and is listed as queued.
	running, label, queued, failed, _ := titleActivityFrom([]jobs.Job{{
		Type:    jobs.TypeDownloadMissing,
		Status:  "queued",
		Payload: `{"title_id":42}`,
	}}, title)
	if failed || label != "" || !running[jobs.TypeDownloadMissing] || len(queued) != 1 || queued[0] != "downloading" {
		t.Fatalf("queued: running %v label %q queued %v failed %v", running, label, queued, failed)
	}
	// A running job surfaces the live label instead.
	running, label, queued, failed, _ = titleActivityFrom([]jobs.Job{{
		Type:    jobs.TypeDownloadMissing,
		Status:  "running",
		Payload: `{"title_id":42}`,
	}}, title)
	if failed || label != "downloading" || len(queued) != 0 || !running[jobs.TypeDownloadMissing] {
		t.Fatalf("running: running %v label %q queued %v failed %v", running, label, queued, failed)
	}
}

func TestTitleActivityFromIgnoresGlobalTitleJob(t *testing.T) {
	t.Parallel()

	running, label, queued, failed, _ := titleActivityFrom([]jobs.Job{{
		Type:    jobs.TypeDownloadMissing,
		Status:  "queued",
		Payload: `{}`,
	}}, library.Title{ID: 42, Monitored: true})
	if len(running) != 0 || label != "" || len(queued) != 0 || failed {
		t.Fatalf("activity = running %v label %q failed %v", running, label, failed)
	}
}

func TestReaderManifestWindow(t *testing.T) {
	t.Parallel()

	progress := library.TitleReadProgress{
		Title: library.Title{ID: 7, DisplayTitle: "Demo"},
		Chapters: []library.ChapterReadStatus{
			{Chapter: library.Chapter{ID: 1, Label: "1"}, TotalPages: 2, FirstUnreadPage: 1},
			{Chapter: library.Chapter{ID: 2, Label: "2"}, TotalPages: 2, FirstUnreadPage: 2, ReadPages: 1},
			{Chapter: library.Chapter{ID: 3, Label: "3"}, TotalPages: 2, FirstUnreadPage: 1},
			{Chapter: library.Chapter{ID: 4, Label: "4"}, TotalPages: 2, FirstUnreadPage: 1},
		},
		NextChapterID: 2,
		NextPage:      2,
	}

	// Hard cutoff: the strip starts at the current chapter (no previous
	// chapters above it); prev remains reachable via navigation only.
	manifest, prevID, nextID := readerManifestWindowMode(progress, 0, false)
	if prevID != 1 || nextID != 3 {
		t.Fatalf("prev/next = %d/%d, want 1/3", prevID, nextID)
	}
	if len(manifest.Chapters) != 2 || manifest.Chapters[0].ID != 2 || manifest.Chapters[1].ID != 3 ||
		manifest.ResumeChapterID != 2 || manifest.ResumePage != 2 {
		t.Fatalf("manifest = %+v, want chapters [2 3] resuming at 2/2", manifest)
	}

	manifest, prevID, nextID = readerManifestWindowMode(progress, 4, false)
	if prevID != 3 || nextID != 0 || len(manifest.Chapters) != 1 || manifest.Chapters[0].ID != 4 {
		t.Fatalf("requested tail manifest = %+v prev=%d next=%d", manifest, prevID, nextID)
	}
}
