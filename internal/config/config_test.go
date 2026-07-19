package config

import (
	"testing"

	"github.com/brogergvhs/kaodoku/internal/browserdownload"
	"github.com/brogergvhs/kaodoku/internal/browserfetch"
)

func TestDownloadDirEnvOverride(t *testing.T) {
	t.Setenv("KAODOKU_DOWNLOAD_DIR", "/downloads")

	cfg, _, err := LoadMerged(Options{IgnoreConfig: true, DownloadDir: "/other"})
	if err != nil {
		t.Fatalf("LoadMerged() error = %v", err)
	}
	if cfg.DownloadDir != "/downloads" {
		t.Fatalf("DownloadDir = %q, want /downloads", cfg.DownloadDir)
	}
}

func TestDownloadDirDefault(t *testing.T) {
	t.Setenv("KAODOKU_DOWNLOAD_DIR", "")

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

func TestBrowserSolverEnvOverride(t *testing.T) {
	t.Setenv("KAODOKU_BROWSER_SOLVER_ENABLED", "true")
	t.Setenv("KAODOKU_BROWSER_SOLVER_PROVIDER", browserfetch.ProviderFlareSolverr)
	t.Setenv("KAODOKU_BROWSER_SOLVER_ENDPOINT", "http://flaresolverr:8191/v1")
	t.Setenv("KAODOKU_BROWSER_SOLVER_TIMEOUT_SECONDS", "120")

	cfg, _, err := LoadMerged(Options{IgnoreConfig: true})
	if err != nil {
		t.Fatalf("LoadMerged() error = %v", err)
	}
	if !cfg.BrowserSolver.Enabled || cfg.BrowserSolver.Provider != browserfetch.ProviderFlareSolverr || cfg.BrowserSolver.Endpoint != "http://flaresolverr:8191/v1" || cfg.BrowserSolver.TimeoutSeconds != 120 {
		t.Fatalf("BrowserSolver = %#v", cfg.BrowserSolver)
	}
}

func TestBrowserDownloaderDefaults(t *testing.T) {
	cfg, _, err := LoadMerged(Options{IgnoreConfig: true})
	if err != nil {
		t.Fatalf("LoadMerged() error = %v", err)
	}
	if cfg.BrowserDownload.Enabled {
		t.Fatal("BrowserDownload.Enabled = true, want false")
	}
	if cfg.BrowserDownload.Endpoint != browserdownload.DefaultEndpoint || cfg.BrowserDownload.TimeoutSeconds != 180 {
		t.Fatalf("BrowserDownload = %#v", cfg.BrowserDownload)
	}
}

func TestBrowserDownloaderEnvOverride(t *testing.T) {
	t.Setenv("KAODOKU_BROWSER_DOWNLOADER_ENABLED", "true")
	t.Setenv("KAODOKU_BROWSER_DOWNLOADER_ENDPOINT", "http://browser-worker:8192")
	t.Setenv("KAODOKU_BROWSER_DOWNLOADER_TIMEOUT_SECONDS", "90")

	cfg, _, err := LoadMerged(Options{IgnoreConfig: true})
	if err != nil {
		t.Fatalf("LoadMerged() error = %v", err)
	}
	if !cfg.BrowserDownload.Enabled || cfg.BrowserDownload.Endpoint != "http://browser-worker:8192" || cfg.BrowserDownload.TimeoutSeconds != 90 {
		t.Fatalf("BrowserDownload = %#v", cfg.BrowserDownload)
	}
}
