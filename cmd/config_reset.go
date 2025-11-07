package cmd

import (
	"fmt"

	"github.com/brogergvhs/mangad/internal/config"

	"github.com/spf13/cobra"
)

var configResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset the current config to default values",
	RunE: func(cmd *cobra.Command, args []string) error {
		activePath, err := config.ActiveConfigPath()
		if err != nil {
			return err
		}

		if err := config.SaveYAML(config.DefaultConfig(), activePath); err != nil {
			return err
		}

		fmt.Printf("Reset active config: %s\n", activePath)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configResetCmd)
}
