package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Repository persists and claims background jobs.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a jobs repository.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Enqueue creates a queued job.
func (r *Repository) Enqueue(ctx context.Context, typ string, payload string, runAfter time.Time) (Job, error) {
	if runAfter.IsZero() {
		runAfter = time.Now()
	}

	row := r.db.QueryRowContext(ctx, `
		INSERT INTO jobs(type, status, payload_json, run_after)
		VALUES (?, 'queued', ?, ?)
		RETURNING id
	`, typ, payload, sqliteTime(runAfter))

	var id int64
	if err := row.Scan(&id); err != nil {
		return Job{}, fmt.Errorf("enqueue job: %w", err)
	}

	return r.Get(ctx, id)
}

// Get returns one job.
func (r *Repository) Get(ctx context.Context, id int64) (Job, error) {
	row := r.db.QueryRowContext(ctx, jobSelect()+` WHERE id = ?`, id)
	job, err := scanJob(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, fmt.Errorf("job %d not found", id)
		}
		return Job{}, fmt.Errorf("get job %d: %w", id, err)
	}

	return job, nil
}

// List returns recent jobs.
func (r *Repository) List(ctx context.Context) ([]Job, error) {
	rows, err := r.db.QueryContext(ctx, jobSelect()+` ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		out = append(out, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate jobs: %w", err)
	}

	return out, nil
}

// ClaimNext marks the next due queued/failed job as running.
func (r *Repository) ClaimNext(ctx context.Context) (Job, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, fmt.Errorf("begin claim: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var id int64
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM jobs
		WHERE status IN ('queued', 'failed') AND run_after <= CURRENT_TIMESTAMP
		ORDER BY run_after, id
		LIMIT 1
	`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("select job to claim: %w", err)
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE jobs
		SET status = 'running', attempts = attempts + 1, last_error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, id); err != nil {
		return Job{}, false, fmt.Errorf("claim job %d: %w", id, err)
	}
	if err = tx.Commit(); err != nil {
		return Job{}, false, fmt.Errorf("commit claim: %w", err)
	}

	job, err := r.Get(ctx, id)
	if err != nil {
		return Job{}, false, err
	}

	return job, true, nil
}

// MarkDone marks a job as completed.
func (r *Repository) MarkDone(ctx context.Context, id int64) error {
	return r.mark(ctx, id, "done", "")
}

// MarkFailed marks a job as failed.
func (r *Repository) MarkFailed(ctx context.Context, id int64, cause error) error {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	return r.mark(ctx, id, "failed", msg)
}

func (r *Repository) mark(ctx context.Context, id int64, status, msg string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE jobs
		SET status = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, msg, id)
	if err != nil {
		return fmt.Errorf("mark job %d %s: %w", id, status, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check job %d %s: %w", id, status, err)
	}
	if rows == 0 {
		return fmt.Errorf("job %d not found", id)
	}

	return nil
}

func jobSelect() string {
	return `
		SELECT id, type, status, payload_json, run_after, attempts, last_error, created_at, updated_at
		FROM jobs
	`
}

type scanner interface {
	Scan(dest ...any) error
}

func scanJob(row scanner) (Job, error) {
	var job Job
	var runAfter, createdAt, updatedAt string
	if err := row.Scan(&job.ID, &job.Type, &job.Status, &job.Payload, &runAfter, &job.Attempts, &job.LastError, &createdAt, &updatedAt); err != nil {
		return Job{}, err
	}

	var err error
	if job.RunAfter, err = parseTime(runAfter); err != nil {
		return Job{}, err
	}
	if job.CreatedAt, err = parseTime(createdAt); err != nil {
		return Job{}, err
	}
	if job.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Job{}, err
	}

	return job, nil
}

func parseTime(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("parse sqlite time %q", value)
}

func sqliteTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04:05")
}
