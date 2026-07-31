package server

import (
	"strings"
	"testing"

	"github.com/brogergvhs/kaodoku/internal/catalog"
	"github.com/brogergvhs/kaodoku/internal/library"
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

func titlesByID(ts []library.Title) map[int64]library.Title {
	m := make(map[int64]library.Title, len(ts))
	for _, t := range ts {
		m[t.ID] = t
	}
	return m
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

func TestAuthorCollectionsMergesIdenticalMemberSets(t *testing.T) {
	t.Parallel()
	titles := []library.Title{title(1, 10, "Gunggwigeomsin 1-bu"), title(2, 20, "Madojeilgeom")}
	mangas := map[int64]catalog.Manga{
		10: {Authors: []string{"Don-Hyeong Jo", "Gwang-Jin Park"}},
		20: {Authors: []string{"Gwang-Jin Park", "Don-Hyeong Jo"}},
	}
	cs := authorCollections(titles, mangas)
	if len(cs) != 1 {
		t.Fatalf("author collections = %+v, want 1 merged", collectionNames(cs))
	}
	if cs[0].Name != "Don-Hyeong Jo, Gwang-Jin Park" {
		t.Errorf("merged name = %q", cs[0].Name)
	}
	if len(cs[0].Members) != 2 {
		t.Errorf("members = %d, want 2", len(cs[0].Members))
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
	cs := relationCollections(titles, mangas, edges, nil, titlesByID(titles))
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
	cs := relationCollections(titles, mangas, edges, nil, titlesByID(titles))
	if len(cs) != 1 || len(cs[0].Members) != 3 {
		t.Fatalf("members = %d, want 3 (both provider-100 siblings + 101)", len(cs[0].Members))
	}
}

func TestRelationCollectionsAppendsSmartPins(t *testing.T) {
	t.Parallel()
	// A GitS cluster (100,101) plus an unrelated title pinned into it.
	titles := []library.Title{
		title(1, 10, "The Ghost in the Shell"),
		title(2, 11, "Ghost in the Shell 1.5"),
		title(3, 20, "Appleseed"),
	}
	mangas := map[int64]catalog.Manga{
		10: {ProviderID: "100"}, 11: {ProviderID: "101"}, 20: {ProviderID: "200"},
	}
	edges := [][2]string{{"100", "101"}}
	// Smart key is the smallest provider id in the component: "100".
	pins := map[string][]int64{"100": {3}}
	cs := relationCollections(titles, mangas, edges, pins, titlesByID(titles))
	if len(cs) != 1 {
		t.Fatalf("relation collections = %+v, want 1", collectionNames(cs))
	}
	if cs[0].SmartKey != "100" {
		t.Errorf("smart key = %q, want 100", cs[0].SmartKey)
	}
	if len(cs[0].Members) != 3 {
		t.Errorf("members = %d, want 3 (2 derived + 1 pinned)", len(cs[0].Members))
	}
	var hasPinned bool
	for _, m := range cs[0].Members {
		if m.DisplayTitle == "Appleseed" {
			hasPinned = true
		}
	}
	if !hasPinned {
		t.Error("pinned title Appleseed should appear in the smart collection")
	}
}

func TestCollectionCardsCarrySmartKey(t *testing.T) {
	t.Parallel()

	cards := collectionCards([]collection{{
		Name:     "Smart",
		SmartKey: "100",
		Members:  []library.Title{{ID: 1, DisplayTitle: "A"}, {ID: 2, DisplayTitle: "B"}},
	}})
	if len(cards) != 1 {
		t.Fatalf("cards = %d, want 1", len(cards))
	}
	if cards[0].SmartKey != "100" || !strings.Contains(cards[0].URL, "smart=100") {
		t.Fatalf("card = %+v", cards[0])
	}
}

func TestCollectionsViewPagination(t *testing.T) {
	t.Parallel()

	v := collectionsView{Mode: "smart", Page: 2, PerPage: 24, Total: 49}
	if v.TotalPages() != 3 || !v.HasPrev() || !v.HasNext() || v.Prev() != 1 || v.Next() != 3 {
		t.Fatalf("pagination = pages %d prev %v next %v", v.TotalPages(), v.HasPrev(), v.HasNext())
	}
	if got := string(v.PageURL(3)); got != "/collections?by=relation&page=3" {
		t.Fatalf("PageURL = %q", got)
	}
}

func TestCustomCollections(t *testing.T) {
	t.Parallel()
	titles := []library.Title{title(1, 10, "A"), title(2, 20, "B"), title(3, 30, "C")}
	cols := []library.Collection{{ID: 5, Name: "My shelf"}}
	members := map[int64][]int64{5: {1, 3, 99}} // 99 is not in the library -> skipped
	cs := customCollections(cols, members, titlesByID(titles))
	if len(cs) != 1 || cs[0].CustomID != 5 || cs[0].Name != "My shelf" {
		t.Fatalf("custom collections = %+v", cs)
	}
	if len(cs[0].Members) != 2 {
		t.Errorf("members = %d, want 2 (missing title filtered out)", len(cs[0].Members))
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
