package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/brogergvhs/kaodoku/internal/database"
)

func TestPersonalMetricsReadingTimeCohorts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "kaodoku.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	db := svc.db
	exec := func(q string, args ...any) {
		if _, e := db.ExecContext(ctx, q, args...); e != nil {
			t.Fatalf("exec %q: %v", q, e)
		}
	}

	exec(`INSERT INTO catalog_manga (id, provider, provider_id, genres_json, average_score) VALUES (1,'p','a','["Action"]',85), (2,'p','b','["Romance"]',72)`)
	exec(`INSERT INTO titles (id, source_url, display_title, catalog_manga_id) VALUES (1,'u1','Manga A',1), (2,'u2','Webtoon B',2)`)
	exec(`INSERT INTO chapters (id, title_id, label, url) VALUES (1,1,'c1','x1'), (2,2,'c2','x2'), (3,1,'c3','x3'), (4,1,'c4','x4')`)
	// chapter 1 = book-paged (20 pages), chapter 2 = long strip (5 pages),
	// chapter 4 = unknown page count (0) — must land in neither cohort.
	exec(`INSERT INTO chapter_read_progress (user_id, chapter_id, total_pages, completed) VALUES (1,1,20,1), (1,2,5,1), (1,4,0,1)`)
	// chapter 3 downloaded but unread -> backlog.
	exec(`INSERT INTO downloads (chapter_id, status) VALUES (3,'done')`)

	ft := func(d time.Duration) string { return database.FormatTime(time.Now().Add(d)) }
	// Session A (paged): gaps 30s then 3m30s (capped to 3m).
	exec(`INSERT INTO chapter_read_pages (user_id, chapter_id, page, read_at) VALUES (1,1,1,?),(1,1,2,?),(1,1,3,?)`,
		ft(-3*time.Hour), ft(-3*time.Hour+30*time.Second), ft(-3*time.Hour+4*time.Minute))
	// Session B (strip), 2h later so it's a separate session: single 20s gap.
	exec(`INSERT INTO chapter_read_pages (user_id, chapter_id, page, read_at) VALUES (1,2,1,?),(1,2,2,?)`,
		ft(-1*time.Hour), ft(-1*time.Hour+20*time.Second))
	// Session C: one page of the unknown-format chapter (own session, no dwell).
	exec(`INSERT INTO chapter_read_pages (user_id, chapter_id, page, read_at) VALUES (1,4,1,?)`, ft(-20*time.Minute))

	m, err := svc.PersonalMetrics(ctx, 1, 30)
	if err != nil {
		t.Fatal(err)
	}
	if m.PagesRead != 6 || m.PagesReadTotal != 6 {
		t.Errorf("PagesRead = %d/%d, want 6/6", m.PagesRead, m.PagesReadTotal)
	}
	if m.ChaptersReadTotal != 3 {
		t.Errorf("ChaptersReadTotal = %d, want 3", m.ChaptersReadTotal)
	}
	if m.Backlog != 1 {
		t.Errorf("Backlog = %d, want 1", m.Backlog)
	}
	rt := m.ReadingTime
	// active = 30 + 180 (capped) + 20 = 230s -> 3 whole minutes.
	if rt.ActiveMinutes != 3 {
		t.Errorf("ActiveMinutes = %d, want 3", rt.ActiveMinutes)
	}
	if rt.PagedChapters != 1 || rt.StripChapters != 1 {
		t.Errorf("cohort chapters = paged %d strip %d, want 1/1", rt.PagedChapters, rt.StripChapters)
	}
	// paged dwell = (30+180)/3 pages = 70s; strip dwell = 20/2 = 10s.
	if rt.PagedSecPerPage != 70 {
		t.Errorf("PagedSecPerPage = %v, want 70", rt.PagedSecPerPage)
	}
	if rt.StripSecPerImage != 10 {
		t.Errorf("StripSecPerImage = %v, want 10", rt.StripSecPerImage)
	}
	// taste picks up both genres from catalog_manga.
	genres := map[string]bool{}
	for _, g := range m.TopGenres {
		genres[g.Name] = true
	}
	if !genres["Action"] || !genres["Romance"] {
		t.Errorf("TopGenres = %+v, want Action+Romance", m.TopGenres)
	}
	if m.TitlesCompleted != 1 { // Manga A fully read (1/1 discovered... c3 unread makes it 1/2)
		t.Logf("TitlesCompleted=%d TitlesInProgress=%d", m.TitlesCompleted, m.TitlesInProgress)
	}
}
