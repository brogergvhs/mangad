package catalog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/brogergvhs/kaodoku/internal/database"
)

func TestReplaceAndReadRelations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "kaodoku.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db)

	if err := repo.ReplaceRelations(ctx, "100", []Relation{
		{ProviderID: "101", Type: "SEQUEL"},
		{ProviderID: "100", Type: "PARENT"}, // self-edge skipped
		{ProviderID: "", Type: "SPIN_OFF"},  // empty skipped
	}); err != nil {
		t.Fatalf("ReplaceRelations() error = %v", err)
	}
	edges, err := repo.CollectionEdges(ctx)
	if err != nil {
		t.Fatalf("CollectionEdges() error = %v", err)
	}
	if len(edges) != 1 || edges[0] != [2]string{"100", "101"} {
		t.Fatalf("edges = %v, want [[100 101]]", edges)
	}

	// Replacing the same from-id clears the old rows.
	if err := repo.ReplaceRelations(ctx, "100", []Relation{{ProviderID: "102", Type: "PREQUEL"}}); err != nil {
		t.Fatal(err)
	}
	edges, _ = repo.CollectionEdges(ctx)
	if len(edges) != 1 || edges[0] != [2]string{"100", "102"} {
		t.Fatalf("after replace edges = %v, want [[100 102]]", edges)
	}
}
