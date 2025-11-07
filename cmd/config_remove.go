package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/brogergvhs/mangad/internal/config"

	"github.com/spf13/cobra"
)

var forceRemove bool

var configRemoveCmd = &cobra.Command{
	Use:   "remove <label>",
	Short: "Remove a config (<config_label>)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		label := args[0]

		active, _ := config.CurrentLabel()

		if label == active && !forceRemove {
			fmt.Printf("Config %q is currently active. Remove it anyway? [y/N]: ", label)

			reader := bufio.NewReader(os.Stdin)
			resp, _ := reader.ReadString('\n')
			resp = strings.TrimSpace(strings.ToLower(resp))

			if resp != "y" && resp != "yes" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		if err := config.RemoveConfig(label, forceRemove); err != nil {
			return err
		}

		fmt.Printf("Removed configuration %q\n", label)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configRemoveCmd)
}
