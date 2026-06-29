package service

import (
	"strings"
	"testing"

	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/sources"
)

func TestCandidateSourceURLsReplacesSlugOnlySample(t *testing.T) {
	urls := candidateSourceURLs(sources.Source{Profile: sources.Profile{
		ID:             "demo",
		SampleMangaURL: "https://demo.test/manga/sample-title/",
	}}, catalog.Manga{
		TitleEnglish: "The Great Mage Returns After 4000 Years",
		Synonyms:     []string{"Archmage Returns"},
	})
	if len(urls) == 0 || urls[0] != "https://demo.test/manga/the-great-mage-returns-after-4000-years" {
		t.Fatalf("urls = %#v", urls)
	}
	if strings.Contains(strings.Join(urls, ","), "sample-title") {
		t.Fatalf("sample slug leaked into urls: %#v", urls)
	}
}

func TestCandidateSourceURLsSkipsNumericOrOpaquePatterns(t *testing.T) {
	for _, sample := range []string{
		"https://mangapill.com/manga/5281/sakamoto-days",
		"https://weebcentral.com/series/01J76XYDMTRNJZJH9G1ADMPJQC/The-Great-Mage-Returns-After-4000-Years",
	} {
		urls := candidateSourceURLs(sources.Source{Profile: sources.Profile{SampleMangaURL: sample}}, catalog.Manga{TitleEnglish: "Demo"})
		if len(urls) != 0 {
			t.Fatalf("candidateSourceURLs(%q) = %#v", sample, urls)
		}
	}
}
