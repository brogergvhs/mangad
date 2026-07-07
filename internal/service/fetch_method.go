package service

import (
	"context"
	"fmt"

	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/ui"
)

// probeConfig wires cfg for a specific fetch backend, ignoring learned state.
func probeConfig(cfg config.Config, src sources.Source, solver, browser bool) config.Config {
	if len(src.AllowedExtensions) > 0 {
		cfg.AllowExt = src.AllowedExtensions
	}
	cfg.BrowserSolver.Enabled = solver
	cfg.BrowserDownload.Enabled = browser
	return cfg
}

// discoverChapters fetches the chapter list, escalating http -> solver, and
// reports the method that worked. The solver is tried only when available.
func discoverChapters(ctx context.Context, cfg config.Config, log ui.Log, src sources.Source, url string, solverAvailable bool) ([]chapters.Chapter, string, error) {
	attempts := []string{sources.FetchHTTP}
	if solverAvailable {
		attempts = append(attempts, sources.FetchSolver)
	}
	var lastErr error
	for _, method := range attempts {
		probe := probeConfig(cfg, src, method == sources.FetchSolver, false)
		svc, err := NewSourceDownloadService(&probe, log, nil, src.Scraper)
		if err != nil {
			return nil, "", err
		}
		list, err := svc.FetchChapters(ctx, url)
		switch {
		case err != nil:
			lastErr = err
		case len(list) == 0:
			lastErr = fmt.Errorf("no chapters found")
		default:
			return list, method, nil
		}
	}
	return nil, "", lastErr
}

// discoverImages extracts sample image URLs using the HTML method the chapter
// list needed. Images present in the HTML download directly (http); their
// absence means the chapter needs Selenium capture (browser), when available.
func discoverImages(ctx context.Context, cfg config.Config, log ui.Log, src sources.Source, chapter chapters.Chapter, chapterMethod string, browserAvailable bool) ([]string, string, error) {
	probe := probeConfig(cfg, src, chapterMethod == sources.FetchSolver, false)
	svc, err := NewSourceDownloadService(&probe, log, nil, src.Scraper)
	if err != nil {
		return nil, "", err
	}
	images, err := svc.FetchImages(ctx, chapter)
	if err != nil {
		return nil, "", err
	}
	if len(images) > 0 {
		return images, sources.FetchHTTP, nil
	}
	if browserAvailable {
		return nil, sources.FetchBrowser, nil
	}
	return nil, "", fmt.Errorf("no images found in sample chapter")
}
