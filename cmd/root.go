package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	flagIgnoreConfig bool
	flagDebug        bool
)

var rootCmd = &cobra.Command{
	Use:   "kaodoku",
	Short: "Manga downloader with CBZ output",
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&flagDebug, "debug", false, "enable debug logging")
	rootCmd.PersistentFlags().BoolVar(&flagIgnoreConfig, "ignore-config", false, "ignore config and use only CLI flags")
}

func Execute() {
	// Cobra prints usage on flag errors; print the error once ourselves.
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
