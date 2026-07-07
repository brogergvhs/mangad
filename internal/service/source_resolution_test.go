package service

import (
	"testing"

	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/sources"
)

func TestMatchSourceForURLMatchesBuiltinDomain(t *testing.T) {
	source, ok := MatchSourceForURL([]sources.Source{
		{
			Profile: sources.Profile{
				ID:             "comickz",
				Domains:        []string{"comickz.co.uk"},
				BaseURL:        "https://comickz.co.uk/",
				SampleMangaURL: "https://comickz.co.uk/comic/demo",
				Scraper:        "comickz",
				Enabled:        true,
			},
			Origin: sources.OriginBuiltin,
		},
	}, "https://comickz.co.uk/comic/02-the-great-mage-returns-after-4000-years")
	if !ok {
		t.Fatal("MatchSourceForURL() did not match")
	}
	if source.ID != "comickz" || source.Scraper != "comickz" {
		t.Fatalf("source = %#v", source)
	}
}

func TestMatchSourceForURLPrefersLocalSource(t *testing.T) {
	builtin := sources.Source{
		Profile: sources.Profile{
			ID:             "builtin",
			Domains:        []string{"example.test"},
			BaseURL:        "https://example.test/",
			SampleMangaURL: "https://example.test/manga/demo",
			Scraper:        "generic",
			Enabled:        true,
		},
		Origin: sources.OriginBuiltin,
	}
	local := builtin
	local.ID = "local"
	local.Scraper = "comickz"
	local.Origin = sources.OriginLocal

	source, ok := MatchSourceForURL([]sources.Source{builtin, local}, "https://example.test/manga/title")
	if !ok {
		t.Fatal("MatchSourceForURL() did not match")
	}
	if source.ID != "local" || source.Scraper != "comickz" {
		t.Fatalf("source = %#v", source)
	}
}

func TestConfigForSourceAppliesRuntimeRequirements(t *testing.T) {
	cfg := *config.DefaultConfig()
	source := sources.Source{Profile: sources.Profile{
		AllowedExtensions:       []string{"webp"},
		RequiresBrowserDownload: true,
		RequiresBrowserSolver:   true,
	}}

	got := ConfigForSource(cfg, source, SourceConfigOptions{})
	if len(got.AllowExt) != 1 || got.AllowExt[0] != "webp" {
		t.Fatalf("AllowExt = %#v", got.AllowExt)
	}
	if !got.BrowserDownload.Enabled || !got.BrowserSolver.Enabled {
		t.Fatalf("browser requirements not applied: %#v", got)
	}
}

func TestConfigForSourceCanPreserveAllowedExtensions(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.AllowExt = []string{"jpg"}
	source := sources.Source{Profile: sources.Profile{AllowedExtensions: []string{"webp"}}}

	got := ConfigForSource(cfg, source, SourceConfigOptions{PreserveAllowedExtensions: true})
	if len(got.AllowExt) != 1 || got.AllowExt[0] != "jpg" {
		t.Fatalf("AllowExt = %#v", got.AllowExt)
	}
}

func TestConfigForSourceLearnedFetchMethods(t *testing.T) {
	base := config.Config{}
	base.BrowserSolver.Enabled = true // runtime has a solver available

	cases := []struct {
		name         string
		src          sources.Source
		wantSolver   bool
		wantDownload bool
	}{
		{
			name:       "learned http disables solver despite hint",
			src:        sources.Source{Profile: sources.Profile{RequiresBrowserSolver: true}, ChapterFetch: sources.FetchHTTP},
			wantSolver: false,
		},
		{
			name:       "learned solver enables solver",
			src:        sources.Source{ChapterFetch: sources.FetchSolver},
			wantSolver: true,
		},
		{
			name:         "learned browser enables image downloader",
			src:          sources.Source{ChapterFetch: sources.FetchHTTP, ImageFetch: sources.FetchBrowser},
			wantSolver:   false,
			wantDownload: true,
		},
		{
			name:       "unknown falls back to profile hint",
			src:        sources.Source{Profile: sources.Profile{RequiresBrowserSolver: true}},
			wantSolver: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ConfigForSource(base, tc.src, SourceConfigOptions{})
			if got.BrowserSolver.Enabled != tc.wantSolver {
				t.Errorf("BrowserSolver.Enabled = %v, want %v", got.BrowserSolver.Enabled, tc.wantSolver)
			}
			if got.BrowserDownload.Enabled != tc.wantDownload {
				t.Errorf("BrowserDownload.Enabled = %v, want %v", got.BrowserDownload.Enabled, tc.wantDownload)
			}
		})
	}
}
