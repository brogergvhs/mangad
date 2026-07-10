package jobs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/brogergvhs/mangad/internal/database"
)

func TestRepositoryQueueLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo := NewRepository(db)
	future, err := repo.Enqueue(ctx, TypeScanDownloads, `{"title_id":2}`, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue(future) error = %v", err)
	}
	due, err := repo.Enqueue(ctx, TypeRefreshTitle, `{"title_id":1}`, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Enqueue(due) error = %v", err)
	}

	job, ok, err := repo.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("ClaimNext() error = %v", err)
	}
	if !ok {
		t.Fatal("ClaimNext() ok = false, want true")
	}
	if job.ID != due.ID || job.Status != "running" || job.Attempts != 1 {
		t.Fatalf("ClaimNext() = %+v, want due running attempt 1", job)
	}

	if err := repo.MarkDone(ctx, job.ID); err != nil {
		t.Fatalf("MarkDone() error = %v", err)
	}
	done, err := repo.Get(ctx, job.ID)
	if err != nil {
		t.Fatalf("Get(done) error = %v", err)
	}
	if done.Status != "done" {
		t.Fatalf("done status = %q, want done", done.Status)
	}

	_, ok, err = repo.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("ClaimNext(no due) error = %v", err)
	}
	if ok {
		t.Fatal("ClaimNext(no due) ok = true, want false")
	}

	// Outcome marks only apply to running jobs.
	if err := repo.MarkFailed(ctx, future.ID, assertErr("boom")); err == nil {
		t.Fatal("MarkFailed(queued job) error = nil, want not-running error")
	}
	jobs, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("List() len = %d, want 2", len(jobs))
	}
}

func TestRepositoryRetryBackoffAndCap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo := NewRepository(db)
	job, err := repo.Enqueue(ctx, TypeRefreshTitle, `{"title_id":1}`, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	for attempt := 1; attempt <= DefaultMaxAttempts; attempt++ {
		claimed, ok, err := repo.ClaimNext(ctx)
		if err != nil {
			t.Fatalf("ClaimNext(%d) error = %v", attempt, err)
		}
		if !ok {
			t.Fatalf("ClaimNext(%d) ok = false", attempt)
		}
		if err := repo.MarkFailed(ctx, claimed.ID, assertErr("boom")); err != nil {
			t.Fatalf("MarkFailed(%d) error = %v", attempt, err)
		}
		got, err := repo.Get(ctx, job.ID)
		if err != nil {
			t.Fatalf("Get(%d) error = %v", attempt, err)
		}
		wantStatus := "failed"
		if attempt == DefaultMaxAttempts {
			wantStatus = "dead"
		}
		if got.Status != wantStatus {
			t.Fatalf("attempt %d status = %q, want %q", attempt, got.Status, wantStatus)
		}
		if attempt < DefaultMaxAttempts && !got.RunAfter.After(time.Now()) {
			t.Fatalf("attempt %d run_after = %s, want future retry", attempt, got.RunAfter)
		}
		_, _ = db.ExecContext(ctx, `UPDATE jobs SET run_after = CURRENT_TIMESTAMP WHERE id = ?`, job.ID)
	}

	_, ok, err := repo.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("ClaimNext(dead) error = %v", err)
	}
	if ok {
		t.Fatal("ClaimNext(dead) ok = true, want false")
	}
}

type assertErr string

func (e assertErr) Error() string {
	return string(e)
}

func TestRepositoryReconcileRunning(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo := NewRepository(db)
	stranded, err := repo.Enqueue(ctx, TypeRefreshTitle, `{"title_id":1}`, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Enqueue(stranded) error = %v", err)
	}
	exhausted, err := repo.Enqueue(ctx, TypeScanDownloads, `{"title_id":2}`, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatalf("Enqueue(exhausted) error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET status = 'running' WHERE id = ?`, stranded.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE jobs SET status = 'running', attempts = ? WHERE id = ?`, DefaultMaxAttempts, exhausted.ID); err != nil {
		t.Fatal(err)
	}

	changed, err := repo.ReconcileRunning(ctx)
	if err != nil {
		t.Fatalf("ReconcileRunning() error = %v", err)
	}
	if changed != 2 {
		t.Fatalf("ReconcileRunning() = %d, want 2", changed)
	}

	requeued, err := repo.Get(ctx, stranded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requeued.Status != "queued" || requeued.LastError != "interrupted" {
		t.Fatalf("stranded job = %q/%q, want queued/interrupted", requeued.Status, requeued.LastError)
	}
	dead, err := repo.Get(ctx, exhausted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dead.Status != "dead" {
		t.Fatalf("exhausted job status = %q, want dead", dead.Status)
	}
}

func TestRepositoryEnqueueDedupesPendingJobs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	repo := NewRepository(db)
	later, err := repo.Enqueue(ctx, TypeRefreshTitle, `{"title_id":1}`, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	same, err := repo.Enqueue(ctx, TypeRefreshTitle, `{"title_id":1}`, time.Now())
	if err != nil {
		t.Fatalf("Enqueue(duplicate) error = %v", err)
	}
	if same.ID != later.ID {
		t.Fatalf("duplicate enqueue ID = %d, want %d", same.ID, later.ID)
	}
	if !same.RunAfter.Before(later.RunAfter) {
		t.Fatalf("run_after not moved forward: %s vs %s", same.RunAfter, later.RunAfter)
	}

	other, err := repo.Enqueue(ctx, TypeRefreshTitle, `{"title_id":2}`, time.Now())
	if err != nil {
		t.Fatalf("Enqueue(other payload) error = %v", err)
	}
	if other.ID == later.ID {
		t.Fatal("different payload reused the same job")
	}

	if _, err := db.ExecContext(ctx, `UPDATE jobs SET status = 'done' WHERE id = ?`, later.ID); err != nil {
		t.Fatal(err)
	}
	fresh, err := repo.Enqueue(ctx, TypeRefreshTitle, `{"title_id":1}`, time.Now())
	if err != nil {
		t.Fatalf("Enqueue(after done) error = %v", err)
	}
	if fresh.ID == later.ID {
		t.Fatal("done job was reused instead of a fresh enqueue")
	}
}

func TestCancelQueuedAndRunning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "mangad.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db)

	// A queued job cancels directly.
	q, err := repo.Enqueue(ctx, TypeRefreshTitle, `{"title_id":1}`, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ok, err := repo.Cancel(ctx, q.ID)
	if err != nil || !ok {
		t.Fatalf("Cancel(queued) ok=%v err=%v", ok, err)
	}
	if got, _ := repo.Get(ctx, q.ID); got.Status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", got.Status)
	}

	// A running job is not cancellable via Cancel (aborted by context instead).
	r2, _ := repo.Enqueue(ctx, TypeRefreshTitle, `{"title_id":2}`, time.Now().Add(-time.Minute))
	if _, _, err := repo.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	ok, err = repo.Cancel(ctx, r2.ID)
	if err != nil || ok {
		t.Fatalf("Cancel(running) ok=%v err=%v, want false", ok, err)
	}
	// MarkCancelled works on the running job.
	if err := repo.MarkCancelled(ctx, r2.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, r2.ID); got.Status != "cancelled" {
		t.Fatalf("running status = %q, want cancelled", got.Status)
	}
}
