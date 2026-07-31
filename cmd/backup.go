package cmd

import (
	"context"
	"fmt"

	"github.com/brogergvhs/kaodoku/internal/database"

	"github.com/spf13/cobra"
)

var flagDataDB string

var backupCmd = &cobra.Command{
	Use:   "backup <file>",
	Short: "Write a user-data backup without downloaded files",
	Args:  cobra.ExactArgs(1),
	RunE:  runBackup,
}

var restoreCmd = &cobra.Command{
	Use:   "restore <file>",
	Short: "Restore a user-data backup without downloaded files",
	Args:  cobra.ExactArgs(1),
	RunE:  runRestore,
}

var exportCmd = &cobra.Command{
	Use:   "export <file>",
	Short: "Export portable user data without downloaded files",
	Args:  cobra.ExactArgs(1),
	RunE:  runBackup,
}

func init() {
	for _, cmd := range []*cobra.Command{backupCmd, restoreCmd, exportCmd} {
		cmd.Flags().StringVar(&flagDataDB, "db", "", "path to Kaodoku SQLite database")
		rootCmd.AddCommand(cmd)
	}
}

func runBackup(_ *cobra.Command, args []string) error {
	if err := database.BackupUserData(context.Background(), flagDataDB, args[0]); err != nil {
		return err
	}
	fmt.Printf("Wrote user-data backup to %s.\n", args[0])
	return nil
}

func runRestore(_ *cobra.Command, args []string) error {
	if err := database.RestoreUserData(context.Background(), flagDataDB, args[0]); err != nil {
		return err
	}
	fmt.Printf("Restored user data from %s.\n", args[0])
	return nil
}
