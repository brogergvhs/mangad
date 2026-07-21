package cmd

import (
	"context"
	"fmt"

	"github.com/brogergvhs/kaodoku/internal/service"

	"github.com/spf13/cobra"
)

var flagRotateDB string

var rotateKeyCmd = &cobra.Command{
	Use:   "rotate-key",
	Short: "Rotate the at-rest encryption key and re-encrypt stored secrets",
	Long: "Generates a fresh kaodoku.key, re-encrypts all stored secrets with it, " +
		"and backs the old key up to kaodoku.key.bak. Stop the server first so no " +
		"write lands on the old key mid-rotation. Refused when the key is pinned " +
		"via KAODOKU_ENCRYPTION_KEY.",
	RunE: func(_ *cobra.Command, _ []string) error {
		n, err := service.RotateEncryptionKey(context.Background(), flagRotateDB)
		if err != nil {
			return err
		}
		fmt.Printf("Rotated encryption key and re-encrypted %d secret(s).\n", n)
		return nil
	},
}

func init() {
	rotateKeyCmd.Flags().StringVar(&flagRotateDB, "db", "", "path to Kaodoku SQLite database")
	configCmd.AddCommand(rotateKeyCmd)
}
