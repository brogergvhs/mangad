package service

import (
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/sources"
)

// probeConfig wires cfg for a specific fetch backend, ignoring learned state.
func probeConfig(cfg config.Config, src sources.Source, solver, browser bool) config.Config {
	if len(src.AllowedExtensions) > 0 {
		cfg.AllowExt = src.AllowedExtensions
	}
	cfg.BrowserSolver.Enabled = solver
	cfg.BrowserDownload.Enabled = browser
	cfg.Chapterless = src.Chapterless
	return cfg
}
