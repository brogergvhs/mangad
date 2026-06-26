package config

import "testing"

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
