package server

import (
	"context"
	"net/url"
	"reflect"
	"testing"

	"github.com/brogergvhs/mangad/internal/auth"
	"github.com/brogergvhs/mangad/internal/catalog"
)

func TestContentAllowedAllowedTags(t *testing.T) {
	t.Parallel()

	ctx := auth.WithUser(context.Background(), &auth.User{
		AllowedTags: []string{"Kids"},
		BlockedTags: []string{"Gore"},
	})
	if !contentAllowed(ctx, false, []string{"Kids", "Action"}) {
		t.Error("kids-tagged manga should be allowed")
	}
	if contentAllowed(ctx, false, []string{"Action"}) {
		t.Error("manga without an allowed tag should be hidden")
	}
	if contentAllowed(ctx, false, nil) {
		t.Error("untagged manga should be hidden under an allowlist")
	}
	if contentAllowed(ctx, false, []string{"Kids", "Gore"}) {
		t.Error("blocked tag should win over the allowlist")
	}
	if contentAllowed(ctx, true, []string{"Kids"}) {
		t.Error("adult flag should still hide content")
	}
	if !contentAllowed(auth.WithUser(context.Background(), &auth.User{}), false, []string{"Action"}) {
		t.Error("no allowlist should keep everything visible")
	}
}

func TestSearchControlsFrom(t *testing.T) {
	t.Parallel()

	c := searchControlsFrom(url.Values{
		"q":            {" one piece "},
		"sort":         {"rating"},
		"dir":          {"asc"},
		"include_tags": {"Action", "Magic, Vampire"},
		"exclude_tags": {"Gore"},
	})
	if c.Q != "one piece" || c.Sort != "rating" || c.Dir != "asc" {
		t.Errorf("q/sort/dir = %q/%q/%q", c.Q, c.Sort, c.Dir)
	}
	if want := []string{"Action", "Magic", "Vampire"}; !reflect.DeepEqual(c.IncludeTags, want) {
		t.Errorf("IncludeTags = %v, want %v", c.IncludeTags, want)
	}

	c = searchControlsFrom(url.Values{"sort": {"bogus"}, "dir": {"sideways"}, "view": {"auto"}})
	if c.Sort != "" || c.Dir != "desc" || c.View != "cards" {
		t.Errorf("unknown sort/dir/view should normalize to relevance/desc/cards, got %q/%q/%q", c.Sort, c.Dir, c.View)
	}
	if c := searchControlsFrom(url.Values{"view": {"table"}}); c.View != "table" {
		t.Errorf("view = %q, want table", c.View)
	}
}

func TestAnilistSort(t *testing.T) {
	t.Parallel()

	cases := []struct{ key, dir, want string }{
		{"", "desc", ""},
		{"rating", "desc", "SCORE_DESC"},
		{"rating", "asc", "SCORE"},
		{"title", "asc", "TITLE_ROMAJI"},
		{"year", "desc", "START_DATE_DESC"},
		{"chapters", "desc", "CHAPTERS_DESC"},
		{"bogus", "desc", ""},
	}
	for _, tc := range cases {
		if got := anilistSort(tc.key, tc.dir); got != tc.want {
			t.Errorf("anilistSort(%q, %q) = %q, want %q", tc.key, tc.dir, got, tc.want)
		}
	}
}

func TestSplitByKind(t *testing.T) {
	t.Parallel()

	options := []catalog.ContentTag{
		{Name: "Action", Kind: "genre"},
		{Name: "Vampire", Kind: "tag"},
	}
	genres, tags := splitByKind([]string{"action", "Vampire", "Unknown"}, options)
	if want := []string{"action"}; !reflect.DeepEqual(genres, want) {
		t.Errorf("genres = %v, want %v", genres, want)
	}
	if want := []string{"Vampire", "Unknown"}; !reflect.DeepEqual(tags, want) {
		t.Errorf("tags = %v, want %v", tags, want)
	}
}

func TestFilterSortManga(t *testing.T) {
	t.Parallel()

	ch := func(n int) *int { return &n }
	items := []catalog.Manga{
		{ProviderID: "1", Genres: []string{"Action"}, Tags: []string{"Vampire"}, AverageScore: 60, Year: 2001, Chapters: ch(10)},
		{ProviderID: "2", Genres: []string{"Action", "Drama"}, AverageScore: 90, Year: 1999},
		{ProviderID: "3", Genres: []string{"Horror"}, AverageScore: 75, Year: 2010, Chapters: ch(3)},
	}
	ids := func(ms []catalog.Manga) []string {
		out := make([]string, 0, len(ms))
		for _, m := range ms {
			out = append(out, m.ProviderID)
		}
		return out
	}

	got := filterSortManga(append([]catalog.Manga(nil), items...), searchControls{
		IncludeTags: []string{"action", "vampire"}, Dir: "desc",
	})
	if want := []string{"1"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("all-of include = %v, want %v", ids(got), want)
	}

	got = filterSortManga(append([]catalog.Manga(nil), items...), searchControls{
		ExcludeTags: []string{"drama"}, Sort: "rating", Dir: "desc",
	})
	if want := []string{"3", "1"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("exclude + rating desc = %v, want %v", ids(got), want)
	}

	got = filterSortManga(append([]catalog.Manga(nil), items...), searchControls{Sort: "year", Dir: "asc"})
	if want := []string{"2", "1", "3"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("year asc = %v, want %v", ids(got), want)
	}

	got = filterSortManga(append([]catalog.Manga(nil), items...), searchControls{Sort: "chapters", Dir: "desc"})
	if want := []string{"1", "3", "2"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("chapters desc = %v, want %v", ids(got), want)
	}

	got = filterSortManga(append([]catalog.Manga(nil), items...), searchControls{Dir: "desc"})
	if want := []string{"1", "2", "3"}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("relevance should keep input order, got %v", ids(got))
	}
}
