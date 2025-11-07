package cmd

import (
	"fmt"

	"github.com/brogergvhs/mangad/internal/config"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var configSwitchCmd = &cobra.Command{
	Use:   "switch [label]",
	Short: "Switch to a different configuration profile",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {

		var label string

		if len(args) == 1 {
			label = args[0]
		} else {
			list, err := config.ListConfigs()
			if err != nil {
				return err
			}
			if len(list) == 0 {
				return fmt.Errorf("no configs available")
			}

			items := []string{}
			for _, c := range list {
				if c.Active {
					items = append(items, c.Label+"  (active)")
				} else {
					items = append(items, c.Label)
				}
			}

			prompt := promptui.Select{
				Label: "Select config",
				Items: items,
			}

			idx, _, err := prompt.Run()
			if err != nil {
				return fmt.Errorf("selection cancelled")
			}

			label = list[idx].Label
		}

		if err := config.SwitchConfig(label); err != nil {
			return err
		}

		fmt.Println("Switched to:", label)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configSwitchCmd)
}
