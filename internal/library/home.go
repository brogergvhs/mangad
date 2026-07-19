package library

import (
	"context"
	"strconv"
	"time"

	"github.com/brogergvhs/kaodoku/internal/auth"
	"github.com/brogergvhs/kaodoku/internal/database"
)

// LastReadAt maps title id to the acting user's most recent reading activity
// across chapters and volumes.
func (r *Repository) LastReadAt(ctx context.Context) (map[int64]time.Time, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT title_id, MAX(at) FROM (
			SELECT c.title_id AS title_id, rp.last_read_at AS at
			FROM chapter_read_progress rp JOIN chapters c ON c.id = rp.chapter_id
			WHERE rp.user_id = ?
			UNION ALL
			SELECT v.title_id, vr.last_read_at
			FROM volume_read_progress vr JOIN volumes v ON v.id = vr.volume_id
			WHERE vr.user_id = ?
		) GROUP BY title_id`, auth.UserID(ctx), auth.UserID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var id int64
		var at string
		if rows.Scan(&id, &at) == nil {
			if t, err := database.ParseTime(at); err == nil {
				out[id] = t
			}
		}
	}
	return out, rows.Err()
}

// Arrival is a title's newest piece of content.
type Arrival struct {
	At    time.Time
	Label string // e.g. "Ch 145" or "Vol 12"
}

// LatestArrivals maps title id to its newest completed chapter download or
// newest volume file.
func (r *Repository) LatestArrivals(ctx context.Context) (map[int64]Arrival, error) {
	out := map[int64]Arrival{}
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.title_id, c.label, d.completed_at
		FROM downloads d
		JOIN chapters c ON c.id = d.chapter_id
		JOIN (
			SELECT c2.title_id AS tid, MAX(d2.completed_at) AS at
			FROM downloads d2 JOIN chapters c2 ON c2.id = d2.chapter_id
			WHERE d2.status = 'completed' AND d2.completed_at IS NOT NULL
			GROUP BY c2.title_id
		) latest ON latest.tid = c.title_id AND latest.at = d.completed_at
		WHERE d.status = 'completed'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var label, at string
		if rows.Scan(&id, &label, &at) == nil {
			if t, err := database.ParseTime(at); err == nil {
				out[id] = Arrival{At: t, Label: "Ch " + label}
			}
		}
	}
	rows.Close()

	rows, err = r.db.QueryContext(ctx, `
		SELECT v.title_id, v.number, v.created_at
		FROM volumes v
		JOIN (SELECT title_id AS tid, MAX(created_at) AS at FROM volumes GROUP BY title_id) latest
			ON latest.tid = v.title_id AND latest.at = v.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var number float64
		var at string
		if rows.Scan(&id, &number, &at) == nil {
			if t, err := database.ParseTime(at); err == nil {
				if cur, ok := out[id]; !ok || t.After(cur.At) {
					out[id] = Arrival{At: t, Label: volumeArrivalLabel(number)}
				}
			}
		}
	}
	return out, rows.Err()
}

func volumeArrivalLabel(number float64) string {
	if number <= 0 {
		return "New volume"
	}
	return "Vol " + strconv.FormatFloat(number, 'f', -1, 64)
}
