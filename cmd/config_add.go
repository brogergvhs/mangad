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

var configAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Create a new config",
	RunE: func(cmd *cobra.Command, args []string) error {

		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Enter label for new config: ")
		label, _ := reader.ReadString('\n')
		label = strings.TrimSpace(label)

		if label == "" {
			return fmt.Errorf("label cannot be empty")
		}

		cfgDir := config.ConfigsDir()
		path := filepath.Join(cfgDir, label+".yaml")

		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("a config named %q already exists", label)
		}

		def := config.DefaultConfig()

		if err := os.MkdirAll(cfgDir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}

		if err := config.SaveYAML(def, path); err != nil {
			return fmt.Errorf("failed to save YAML: %w", err)
		}

		fmt.Printf("Created new config: %s\n", path)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configAddCmd)
}
