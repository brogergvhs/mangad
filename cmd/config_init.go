package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brogergvhs/mangad/internal/config"

	"github.com/spf13/cobra"
)

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create the Default config",
	RunE: func(cmd *cobra.Command, args []string) error {

		cfgDir := config.ConfigsDir()
		defaultPath := filepath.Join(cfgDir, "Default.yaml")

		if _, err := os.Stat(defaultPath); err == nil {
			fmt.Println("Configuration already exists at:")
			fmt.Println("  ", defaultPath)
			fmt.Println("Use `mangad config reset` to recreate it.")
			return nil
		}

		def := config.DefaultConfig()

		fmt.Println("Configuration file will be saved at:")
		fmt.Println("  ", defaultPath)
		fmt.Println()

		fmt.Println("Default configuration:")
		def.Print()
		fmt.Println()

		reader := bufio.NewReader(os.Stdin)
		fmt.Printf("Create Default config at %s? [y/N]: ", defaultPath)
		resp, _ := reader.ReadString('\n')
		resp = strings.TrimSpace(strings.ToLower(resp))

		if resp != "y" && resp != "yes" {
			fmt.Println("Aborted.")
			return nil
		}

		if err := os.MkdirAll(cfgDir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		if err := config.SaveYAML(def, defaultPath); err != nil {
			return fmt.Errorf("failed to write config file: %w", err)
		}

		if err := config.SwitchConfig("Default"); err != nil {
			return fmt.Errorf("failed to set active config: %w", err)
		}

		fmt.Println("Config created at:", defaultPath)
		fmt.Println("This config is now active (label: Default).")

		return nil
	},
}

func init() {
	configCmd.AddCommand(configInitCmd)
}
