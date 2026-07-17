package catalog

import (
	"reflect"
	"strings"
	"testing"
)

func TestSearchQueryVarsPlain(t *testing.T) {
	t.Parallel()

	gql, vars := searchQueryVars("naruto", 12, SearchFilter{})
	if strings.Contains(gql, "genre_in") || strings.Contains(gql, "tag_in") || strings.Contains(gql, "sort: $sort") {
		t.Errorf("plain search should not declare filter args:\n%s", gql)
	}
	for _, arg := range []string{"search: $search", "Page(page: $page", "pageInfo { hasNextPage }"} {
		if !strings.Contains(gql, arg) {
			t.Errorf("query missing %q:\n%s", arg, gql)
		}
	}
	if want := map[string]any{"search": "naruto", "perPage": 12, "page": 1}; !reflect.DeepEqual(vars, want) {
		t.Errorf("vars = %v, want %v", vars, want)
	}
}

func TestSearchHasMore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		hasNext     bool
		page, limit int
		want        bool
	}{
		{true, 1, 18, true},
		{false, 1, 18, false},
		{true, 276, 18, true},  // 277*18 = 4986 <= 5000
		{true, 277, 18, false}, // 278*18 = 5004 > 5000
		{true, 0, 18, true},    // unset page treated as 1
	}
	for _, tc := range cases {
		if got := searchHasMore(tc.hasNext, tc.page, tc.limit); got != tc.want {
			t.Errorf("searchHasMore(%v, %d, %d) = %v, want %v", tc.hasNext, tc.page, tc.limit, got, tc.want)
		}
	}
}

func TestSearchQueryVarsBrowse(t *testing.T) {
	t.Parallel()

	gql, vars := searchQueryVars("", 18, SearchFilter{GenreIn: []string{"Cyberpunk"}, Sort: "POPULARITY_DESC", Page: 3})
	if strings.Contains(gql, "search: $search") {
		t.Errorf("browse without a query must not declare the search arg:\n%s", gql)
	}
	if _, ok := vars["search"]; ok {
		t.Error("search var should be absent for a browse")
	}
	if vars["page"] != 3 {
		t.Errorf("page = %v, want 3", vars["page"])
	}
	if !strings.Contains(gql, "genre_in: $genreIn") || !strings.Contains(gql, "sort: $sort") {
		t.Errorf("browse filter args missing:\n%s", gql)
	}
}

func TestSearchQueryVarsFiltered(t *testing.T) {
	t.Parallel()

	f := SearchFilter{
		GenreIn:    []string{"Action"},
		GenreNotIn: []string{"Romance"},
		TagIn:      []string{"Vampire"},
		TagNotIn:   []string{"Gore"},
		Sort:       "SCORE_DESC",
	}
	gql, vars := searchQueryVars("naruto", 12, f)
	for _, arg := range []string{
		"$genreIn: [String]", "genre_in: $genreIn",
		"$genreNotIn: [String]", "genre_not_in: $genreNotIn",
		"$tagIn: [String]", "tag_in: $tagIn",
		"$tagNotIn: [String]", "tag_not_in: $tagNotIn",
		"$minTagRank: Int", "minimumTagRank: $minTagRank",
		"$sort: [MediaSort]", "sort: $sort",
	} {
		if !strings.Contains(gql, arg) {
			t.Errorf("query missing %q:\n%s", arg, gql)
		}
	}
	if !reflect.DeepEqual(vars["genreIn"], []string{"Action"}) || !reflect.DeepEqual(vars["tagNotIn"], []string{"Gore"}) {
		t.Errorf("filter vars not threaded: %v", vars)
	}
	if vars["minTagRank"] != 0 {
		t.Errorf("minTagRank = %v, want 0", vars["minTagRank"])
	}
	if !reflect.DeepEqual(vars["sort"], []string{"SCORE_DESC"}) {
		t.Errorf("sort var = %v, want [SCORE_DESC]", vars["sort"])
	}
}

func TestSearchQueryVarsGenreOnly(t *testing.T) {
	t.Parallel()

	gql, vars := searchQueryVars("naruto", 12, SearchFilter{GenreIn: []string{"Action"}})
	if !strings.Contains(gql, "genre_in: $genreIn") {
		t.Errorf("query missing genre_in:\n%s", gql)
	}
	for _, absent := range []string{"tag_in", "tag_not_in", "minimumTagRank", "sort: $sort"} {
		if strings.Contains(gql, absent) {
			t.Errorf("genre-only query should not declare %q:\n%s", absent, gql)
		}
	}
	if _, ok := vars["minTagRank"]; ok {
		t.Error("minTagRank should be absent without tag filters")
	}
}
