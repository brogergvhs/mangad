package config

import (
	"testing"

	"github.com/brogergvhs/mangad/internal/browserfetch"
)

func TestDownloadDirEnvOverride(t *testing.T) {
	t.Setenv("MANGAD_DOWNLOAD_DIR", "/downloads")

	cfg, _, err := LoadMerged(Options{IgnoreConfig: true, DownloadDir: "/other"})
	if err != nil {
		t.Fatalf("LoadMerged() error = %v", err)
	}
	if cfg.DownloadDir != "/downloads" {
		t.Fatalf("DownloadDir = %q, want /downloads", cfg.DownloadDir)
	}
}

func TestDownloadDirDefault(t *testing.T) {
	t.Setenv("MANGAD_DOWNLOAD_DIR", "")

	cfg, _, err := LoadMerged(Options{IgnoreConfig: true})
	if err != nil {
		t.Fatalf("LoadMerged() error = %v", err)
	}
	if cfg.DownloadDir != "." {
		t.Fatalf("DownloadDir = %q, want .", cfg.DownloadDir)
	}
}

func TestBrowserSolverDefaults(t *testing.T) {
	cfg, _, err := LoadMerged(Options{IgnoreConfig: true})
	if err != nil {
		t.Fatalf("LoadMerged() error = %v", err)
	}
	if cfg.BrowserSolver.Enabled {
		t.Fatal("BrowserSolver.Enabled = true, want false")
	}
	if cfg.BrowserSolver.Provider != browserfetch.ProviderFlareSolverr {
		t.Fatalf("BrowserSolver.Provider = %q", cfg.BrowserSolver.Provider)
	}
	if cfg.BrowserSolver.Endpoint == "" || cfg.BrowserSolver.TimeoutSeconds != 60 {
		t.Fatalf("BrowserSolver = %#v", cfg.BrowserSolver)
	}
}
