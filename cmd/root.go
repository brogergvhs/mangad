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
	Use:   "mangad",
	Short: "Manga downloader with CBZ output",
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&flagDebug, "debug", false, "enable debug logging")
	rootCmd.PersistentFlags().BoolVar(&flagIgnoreConfig, "ignore-config", false, "ignore config and use only CLI flags")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
