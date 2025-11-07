package cmd

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/brogergvhs/mangad/internal/config"

	"github.com/spf13/cobra"
)

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available configs",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgDir := config.ConfigsDir()
		active, err := config.ActiveConfigPath()
		if err != nil {
			log.Print(err)
		}

		entries, err := os.ReadDir(cfgDir)
		if err != nil {
			return fmt.Errorf("cannot read configs directory: %w", err)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 4, ' ', 0)
		_, _ = fmt.Fprintln(w, "LABEL\tPATH\tACTIVE")

		var rows []string

		for _, e := range entries {
			if e.IsDir() {
				continue
			}

			name := e.Name()
			label := strings.TrimSuffix(name, filepath.Ext(name))
			path := filepath.Join(cfgDir, name)

			activeMark := ""
			if path == active {
				activeMark = "yes"
			}

			rows = append(rows, fmt.Sprintf("%s\t%s\t%s", label, path, activeMark))
		}

		sort.Strings(rows)

		for _, r := range rows {
			_, _ = fmt.Fprintln(w, r)
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
