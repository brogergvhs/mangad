package service

import (
	"context"
	"time"

	"github.com/brogergvhs/kaodoku/internal/database"
)

// maxNotifications caps stored history; older ones are pruned on insert.
const maxNotifications = 200

// Notification is a system event; UserID 0 means server-wide.
type Notification struct {
	ID        int64
	UserID    int64
	Level     string // "error" | "warn" | "info"
	Message   string
	JobID     int64
	ReadAt    string // "" = unread
	CreatedAt string
}

// NotificationScope bounds which notifications a user may see: always their
// own, server-wide with Server, and every user's with All.
type NotificationScope struct {
	UserID int64
	Server bool
	All    bool
}

func (sc NotificationScope) where() (string, []any) {
	switch {
	case sc.All:
		return "1=1", nil
	case sc.Server:
		return "user_id IN (0, ?)", []any{sc.UserID}
	default:
		return "user_id = ?", []any{sc.UserID}
	}
}

// AddNotification records an event (userID 0 = server-wide), pruning to maxNotifications.
func (s *JobService) AddNotification(ctx context.Context, userID int64, level, message string, jobID int64) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO notifications (user_id, level, message, job_id) VALUES (?, ?, ?, ?)`,
		userID, level, message, jobID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE id NOT IN (SELECT id FROM notifications ORDER BY id DESC LIMIT ?)`,
		maxNotifications)
	return err
}

// Notifications returns recent notifications visible in scope, newest first.
func (s *JobService) Notifications(ctx context.Context, sc NotificationScope, limit int) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	where, args := sc.where()
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, user_id, level, message, job_id, read_at, created_at FROM notifications WHERE `+where+` ORDER BY id DESC LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		if rows.Scan(&n.ID, &n.UserID, &n.Level, &n.Message, &n.JobID, &n.ReadAt, &n.CreatedAt) == nil {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

// UnreadNotificationCount counts unread notifications visible in scope.
func (s *JobService) UnreadNotificationCount(ctx context.Context, sc NotificationScope) (int, error) {
	where, args := sc.where()
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE read_at = '' AND `+where, args...).Scan(&n)
	return n, err
}

// MarkNotificationsRead marks all unread notifications in scope as read.
func (s *JobService) MarkNotificationsRead(ctx context.Context, sc NotificationScope) error {
	where, args := sc.where()
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET read_at = ? WHERE read_at = '' AND `+where,
		append([]any{database.FormatTime(time.Now())}, args...)...)
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
