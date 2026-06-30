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
