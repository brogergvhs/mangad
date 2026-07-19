package server

import (
	"testing"

	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/library"
)

func title(id int64, catID int64, name string) library.Title {
	return library.Title{ID: id, CatalogMangaID: &catID, DisplayTitle: name}
}

func collectionNames(cs []collection) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}

func TestAuthorCollections(t *testing.T) {
	t.Parallel()
	titles := []library.Title{title(1, 10, "BLAME!"), title(2, 20, "Aposimz"), title(3, 30, "Berserk"), title(4, 40, "Solo")}
	mangas := map[int64]catalog.Manga{
		10: {Authors: []string{"Tsutomu Nihei"}},
		20: {Authors: []string{"tsutomu nihei"}}, // case variant, must merge
		30: {Authors: []string{"Kentaro Miura"}}, // lone author -> dropped
		40: {Authors: nil},
	}
	cs := authorCollections(titles, mangas)
	if len(cs) != 1 || cs[0].Name != "Tsutomu Nihei" {
		t.Fatalf("author collections = %+v", collectionNames(cs))
	}
	if len(cs[0].Members) != 2 {
		t.Errorf("Nihei members = %d, want 2", len(cs[0].Members))
	}
}

func TestRelationCollectionsComponentsAndSingletons(t *testing.T) {
	t.Parallel()
	// Ghost in the Shell cluster (100,101,102) + an isolated Overgeared (200)
	// whose only edge points to a title not in the library (999).
	titles := []library.Title{
		title(1, 10, "The Ghost in the Shell"),
		title(2, 11, "Ghost in the Shell 1.5"),
		title(3, 12, "The Ghost in the Shell: The Human Algorithm"),
		title(4, 20, "Overgeared"),
	}
	mangas := map[int64]catalog.Manga{
		10: {ProviderID: "100"}, 11: {ProviderID: "101"}, 12: {ProviderID: "102"},
		20: {ProviderID: "200"},
	}
	edges := [][2]string{
		{"100", "101"}, // chain within the cluster
		{"101", "102"},
		{"200", "999"}, // Overgeared -> a title not in library
	}
	cs := relationCollections(titles, mangas, edges)
	if len(cs) != 1 {
		t.Fatalf("relation collections = %+v, want 1 (singleton Overgeared dropped)", collectionNames(cs))
	}
	if len(cs[0].Members) != 3 {
		t.Errorf("cluster size = %d, want 3", len(cs[0].Members))
	}
	if cs[0].Name != "Ghost in the Shell 1.5" { // shortest member title
		t.Errorf("collection name = %q", cs[0].Name)
	}
	for _, m := range cs[0].Members {
		if m.DisplayTitle == "Overgeared" {
			t.Error("Overgeared must not appear in any collection")
		}
	}
}

func TestRelationCollectionsKeepsSiblingsSharingProviderID(t *testing.T) {
	t.Parallel()
	// Two library titles point to the same catalog manga (same provider id 100),
	// plus a related title (101). All three must appear, not just the last.
	titles := []library.Title{
		title(1, 10, "GitS (source A)"),
		title(2, 10, "GitS (source B)"),
		title(3, 11, "GitS 1.5"),
	}
	mangas := map[int64]catalog.Manga{10: {ProviderID: "100"}, 11: {ProviderID: "101"}}
	edges := [][2]string{{"100", "101"}}
	cs := relationCollections(titles, mangas, edges)
	if len(cs) != 1 || len(cs[0].Members) != 3 {
		t.Fatalf("members = %d, want 3 (both provider-100 siblings + 101)", len(cs[0].Members))
	}
}

func TestCollectionRelationTypes(t *testing.T) {
	t.Parallel()
	for _, ty := range []string{"SEQUEL", "PREQUEL", "SIDE_STORY", "ALTERNATIVE", "SPIN_OFF"} {
		if !catalog.CollectionRelation(ty) {
			t.Errorf("%s should count as a collection relation", ty)
		}
	}
	for _, ty := range []string{"ADAPTATION", "CHARACTER", "OTHER", "SOURCE"} {
		if catalog.CollectionRelation(ty) {
			t.Errorf("%s should NOT count as a collection relation", ty)
		}
	}
}
