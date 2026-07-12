package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

// COMPLETED is only pushed when every chapter is read AND the series has
// finished releasing; a caught-up ongoing series stays CURRENT.
func TestLocalAniListState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()

	seed := func(t *testing.T, key, release string, chapters, read int) int64 {
		t.Helper()
		res, err := svc.db.ExecContext(ctx, `INSERT INTO catalog_manga (provider, provider_id, title_romaji, status) VALUES ('anilist', ?, 't', ?)`, key, release)
		if err != nil {
			t.Fatal(err)
		}
		cid, _ := res.LastInsertId()
		res, err = svc.db.ExecContext(ctx, `INSERT INTO titles (catalog_manga_id, source_url, display_title) VALUES (?, ?, 't')`, cid, "seed://"+key)
		if err != nil {
			t.Fatal(err)
		}
		titleID, _ := res.LastInsertId()
		for i := 1; i <= chapters; i++ {
			res, err = svc.db.ExecContext(ctx, `INSERT INTO chapters (title_id, label, url, number_main) VALUES (?, ?, ?, ?)`,
				titleID, i, fmt.Sprintf("seed://%s/%d", key, i), i)
			if err != nil {
				t.Fatal(err)
			}
			if i <= read {
				chID, _ := res.LastInsertId()
				if _, err := svc.db.ExecContext(ctx, `INSERT INTO chapter_read_progress (user_id, chapter_id, completed) VALUES (1, ?, 1)`, chID); err != nil {
					t.Fatal(err)
				}
			}
		}
		return titleID
	}

	cases := []struct {
		name         string
		release      string
		chapters     int
		read         int
		wantProgress int
		wantStatus   string
	}{
		{"nothing read", "RELEASING", 5, 0, 0, "PLANNING"},
		{"partially read", "RELEASING", 5, 3, 3, "CURRENT"},
		{"caught up but still releasing", "RELEASING", 5, 5, 5, "CURRENT"},
		{"all read and finished", "FINISHED", 5, 5, 5, "COMPLETED"},
		{"partially read finished series", "FINISHED", 5, 2, 2, "CURRENT"},
		{"no chapters discovered", "FINISHED", 0, 0, 0, "PLANNING"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			titleID := seed(t, fmt.Sprintf("k%d", i), tc.release, tc.chapters, tc.read)
			progress, status, err := svc.localAniListState(ctx, 1, titleID)
			if err != nil {
				t.Fatal(err)
			}
			if progress != tc.wantProgress || status != tc.wantStatus {
				t.Fatalf("localAniListState = (%d, %q), want (%d, %q)", progress, status, tc.wantProgress, tc.wantStatus)
			}
		})
	}
}
