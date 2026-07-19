package cmd

import (
	"github.com/brogergvhs/kaodoku/internal/config"
	"github.com/brogergvhs/kaodoku/internal/ui"
)

func runtimeConfig() (*config.Config, ui.Log, error) {
	cfg, _, err := config.LoadMerged(config.Options{IgnoreConfig: flagIgnoreConfig, Debug: flagDebug})
	if err != nil {
		return nil, nil, err
	}
	return cfg, ui.NewLogger(cfg.Debug), nil
}
