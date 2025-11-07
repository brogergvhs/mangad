package cmd

import (
	"fmt"

	"github.com/brogergvhs/mangad/internal/config"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage the config files for the mangad",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, used, err := config.LoadMerged(config.Options{
			IgnoreConfig: flagIgnoreConfig,
			Debug:        flagDebug,
		})
		if err != nil {
			return err
		}

		fmt.Printf("Loaded config from:\n  %s\n\n", used)
		cfg.Print()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
}
