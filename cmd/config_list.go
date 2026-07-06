package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/brogergvhs/mangad/internal/config"

	"github.com/spf13/cobra"
)

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available configs",
	RunE: func(cmd *cobra.Command, args []string) error {
		list, err := config.ListConfigs()
		if err != nil {
			return err
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
		_, _ = fmt.Fprintln(w, "LABEL\tPATH\tACTIVE")
		for _, info := range list {
			activeMark := ""
			if info.Active {
				activeMark = "yes"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", info.Label, info.Path, activeMark)
		}
		if err := w.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to flush table output: %v\n", err)
		}
		return nil
	},
}

func init() {
	configCmd.AddCommand(configListCmd)
}
