package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show the mangad version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("mangad version:", Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
