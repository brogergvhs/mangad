package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/brogergvhs/mangad/internal/config"

	"github.com/spf13/cobra"
)

var configEditCmd = &cobra.Command{
	Use:   "edit (optional <config_label>)",
	Short: "Edit current or specified config",
	RunE: func(cmd *cobra.Command, args []string) error {
		var label string

		if len(args) == 0 {
			var err error
			label, err = config.CurrentLabel()
			if err != nil {
				return fmt.Errorf("failed to get current config label: %w", err)
			}
		} else {
			label = args[0]
		}

		path, err := config.ConfigPathByLabel(label)
		if err != nil {
			return fmt.Errorf("failed to get config path: %w", err)
		}

		cmdExec := exec.Command("nvim", path)
		cmdExec.Stdin = os.Stdin
		cmdExec.Stdout = os.Stdout
		cmdExec.Stderr = os.Stderr

		if err := cmdExec.Run(); err != nil {
			return fmt.Errorf("failed to open editor: %w", err)
		}

		return nil
	},
}

func init() {
	configCmd.AddCommand(configEditCmd)
}
