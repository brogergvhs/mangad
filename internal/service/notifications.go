package service

import (
	"context"
	"time"

	"github.com/brogergvhs/kaodoku/internal/database"
)

// maxNotifications caps stored history; older ones are pruned on insert.
const maxNotifications = 200

// Notification is a global (not per-user) system event, e.g. a terminal job failure.
type Notification struct {
	ID        int64
	Level     string // "error" | "warn" | "info"
	Message   string
	JobID     int64
	ReadAt    string // "" = unread
	CreatedAt string
}

// AddNotification records an event, pruning to maxNotifications.
func (s *JobService) AddNotification(ctx context.Context, level, message string, jobID int64) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO notifications (level, message, job_id) VALUES (?, ?, ?)`,
		level, message, jobID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE id NOT IN (SELECT id FROM notifications ORDER BY id DESC LIMIT ?)`,
		maxNotifications)
	return err
}

// Notifications returns recent notifications, newest first.
func (s *JobService) Notifications(ctx context.Context, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, level, message, job_id, read_at, created_at FROM notifications ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if rows.Scan(&n.ID, &n.Level, &n.Message, &n.JobID, &n.ReadAt, &n.CreatedAt) == nil {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

// UnreadNotificationCount counts unread notifications.
func (s *JobService) UnreadNotificationCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE read_at = ''`).Scan(&n)
	return n, err
}

// MarkNotificationsRead marks all unread as read.
func (s *JobService) MarkNotificationsRead(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = ? WHERE read_at = ''`, database.FormatTime(time.Now()))
	return err
}

// DeleteNotification removes one notification.
func (s *JobService) DeleteNotification(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notifications WHERE id = ?`, id)
	return err
}

// ClearNotifications removes all notifications.
func (s *JobService) ClearNotifications(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notifications`)
	return err
}

// truncateError bounds a message, cutting on a rune boundary (valid UTF-8).
func truncateError(msg string) string {
	const max = 300
	r := []rune(msg)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return msg
}
