package library

import (
	"context"
	"fmt"
	"strings"

	"github.com/brogergvhs/kaodoku/internal/auth"
)

// Collection is a user-created, manually curated group of titles.
type Collection struct {
	ID   int64
	Name string
}

// Collections lists the acting user's custom collections.
func (r *Repository) Collections(ctx context.Context) ([]Collection, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name FROM collections WHERE user_id = ? ORDER BY name COLLATE NOCASE, id`, auth.UserID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CreateCollection creates an empty custom collection for the acting user.
func (r *Repository) CreateCollection(ctx context.Context, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, fmt.Errorf("a collection name is required")
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO collections (user_id, name) VALUES (?, ?)`, auth.UserID(ctx), name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RenameCollection renames one of the acting user's collections.
func (r *Repository) RenameCollection(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("a collection name is required")
	}
	_, err := r.db.ExecContext(ctx, `UPDATE collections SET name = ? WHERE id = ? AND user_id = ?`, name, id, auth.UserID(ctx))
	return err
}

// DeleteCollection removes one of the acting user's collections; its members
// are dropped by cascade.
func (r *Repository) DeleteCollection(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM collections WHERE id = ? AND user_id = ?`, id, auth.UserID(ctx))
	return err
}

// AddCollectionMember adds a title to a collection the acting user owns.
func (r *Repository) AddCollectionMember(ctx context.Context, collectionID, titleID int64) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO collection_members (collection_id, title_id)
		SELECT ?, ? WHERE EXISTS (SELECT 1 FROM collections WHERE id = ? AND user_id = ?)`,
		collectionID, titleID, collectionID, auth.UserID(ctx))
	return err
}

// RemoveCollectionMember removes a title from a collection the acting user owns.
func (r *Repository) RemoveCollectionMember(ctx context.Context, collectionID, titleID int64) error {
	_, err := r.db.ExecContext(ctx, `
		DELETE FROM collection_members
		WHERE collection_id = ? AND title_id = ?
		  AND collection_id IN (SELECT id FROM collections WHERE user_id = ?)`,
		collectionID, titleID, auth.UserID(ctx))
	return err
}

// CollectionMembers returns collection id -> member title ids for the acting
// user's collections.
func (r *Repository) CollectionMembers(ctx context.Context) (map[int64][]int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT cm.collection_id, cm.title_id
		FROM collection_members cm
		JOIN collections c ON c.id = cm.collection_id
		WHERE c.user_id = ?`, auth.UserID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]int64{}
	for rows.Next() {
		var cid, tid int64
		if err := rows.Scan(&cid, &tid); err != nil {
			return nil, err
		}
		out[cid] = append(out[cid], tid)
	}
	return out, rows.Err()
}

// SmartPins returns smart-collection key -> pinned title ids for the acting user.
func (r *Repository) SmartPins(ctx context.Context) (map[string][]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT smart_key, title_id FROM collection_smart_pins WHERE user_id = ?`, auth.UserID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]int64{}
	for rows.Next() {
		var key string
		var tid int64
		if err := rows.Scan(&key, &tid); err != nil {
			return nil, err
		}
		out[key] = append(out[key], tid)
	}
	return out, rows.Err()
}

// AddSmartPin pins a title into an auto-derived (smart) collection.
func (r *Repository) AddSmartPin(ctx context.Context, smartKey string, titleID int64) error {
	if strings.TrimSpace(smartKey) == "" {
		return fmt.Errorf("a smart collection is required")
	}
	_, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO collection_smart_pins (user_id, smart_key, title_id) VALUES (?, ?, ?)`, auth.UserID(ctx), smartKey, titleID)
	return err
}
