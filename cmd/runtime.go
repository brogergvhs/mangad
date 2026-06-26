package cmd

import (
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/ui"
)

func runtimeConfig() (*config.Config, *ui.Logger, error) {
	cfg, _, err := config.LoadMerged(config.Options{IgnoreConfig: flagIgnoreConfig, Debug: flagDebug})
	if err != nil {
		return nil, nil, err
	}
	return cfg, ui.NewLogger(cfg.Debug), nil
}
