package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrNoConfig = errors.New("no config selected")

func ConfigRoot() string {
	// Windows
	if appdata := os.Getenv("APPDATA"); appdata != "" {
		return filepath.Join(appdata, "mangad")
	}

	// Linux/macOS XDG
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "mangad")
	}

	// Linux/macOS default
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "mangad")
}

func ConfigsDir() string {
	return filepath.Join(ConfigRoot(), "configs")
}

func CurrentLabelFile() string {
	return filepath.Join(ConfigRoot(), "current_config")
}

func ensureDirs() error {
	if err := os.MkdirAll(ConfigRoot(), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(ConfigsDir(), 0755); err != nil {
		return err
	}
	return nil
}

func CurrentLabel() (string, error) {
	if err := ensureDirs(); err != nil {
		return "", err
	}

	b, err := os.ReadFile(CurrentLabelFile())
	if os.IsNotExist(err) {
		return "", ErrNoConfig
	}
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(b)), nil
}

func ActiveConfigPath() (string, error) {
	if err := ensureDirs(); err != nil {
		return "", err
	}

	label, err := CurrentLabel()
	if err != nil || label == "" {
		return "", ErrNoConfig
	}

	return filepath.Join(ConfigsDir(), label+".yaml"), nil
}

type ConfigInfo struct {
	Label  string
	Path   string
	Active bool
}

func ListConfigs() ([]ConfigInfo, error) {
	if err := ensureDirs(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(ConfigsDir())
	if err != nil {
		return nil, err
	}

	activeLabel, _ := CurrentLabel()
	var out []ConfigInfo

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}

		label := strings.TrimSuffix(name, ".yaml")
		out = append(out, ConfigInfo{
			Label:  label,
			Path:   filepath.Join(ConfigsDir(), name),
			Active: label == activeLabel,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

func SwitchConfig(label string) error {
	if strings.TrimSpace(label) == "" {
		return errors.New("label cannot be empty")
	}
	if err := ensureDirs(); err != nil {
		return err
	}

	cfgPath := filepath.Join(ConfigsDir(), label+".yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		return fmt.Errorf("config %q does not exist", cfgPath)
	}

	return os.WriteFile(CurrentLabelFile(), []byte(label), 0644)
}

func AddConfig(label, srcPath string) error {
	if strings.TrimSpace(label) == "" {
		return errors.New("label cannot be empty")
	}
	if err := ensureDirs(); err != nil {
		return err
	}

	dst := filepath.Join(ConfigsDir(), label+".yaml")
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("config %q already exists", label)
	}

	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}

	return os.WriteFile(dst, raw, 0644)
}

func CreateEmptyConfig(label string) (string, error) {
	if strings.TrimSpace(label) == "" {
		return "", errors.New("label cannot be empty")
	}
	if err := ensureDirs(); err != nil {
		return "", err
	}

	path := filepath.Join(ConfigsDir(), label+".yaml")

	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("config %q already exists", label)
	}

	if err := SaveYAML(DefaultConfig(), path); err != nil {
		return "", err
	}

	return path, nil
}

func RenameConfig(oldLabel, newLabel string) error {
	if strings.TrimSpace(newLabel) == "" {
		return errors.New("new label cannot be empty")
	}
	if err := ensureDirs(); err != nil {
		return err
	}

	oldPath := filepath.Join(ConfigsDir(), oldLabel+".yaml")
	newPath := filepath.Join(ConfigsDir(), newLabel+".yaml")

	if _, err := os.Stat(oldPath); err != nil {
		return fmt.Errorf("config %q does not exist", oldLabel)
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("config %q already exists", newLabel)
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		return err
	}

	active, _ := CurrentLabel()
	if active == oldLabel {
		return os.WriteFile(CurrentLabelFile(), []byte(newLabel), 0644)
	}

	return nil
}

func RemoveConfig(label string, force bool) error {
	if strings.TrimSpace(label) == "" {
		return errors.New("label cannot be empty")
	}
	if label == "Default" {
		return errors.New("cannot remove the Default config")
	}
	if err := ensureDirs(); err != nil {
		return err
	}

	path := filepath.Join(ConfigsDir(), label+".yaml")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("config %q does not exist", label)
	}

	active, _ := CurrentLabel()
	if active == label {
		if err := SwitchConfig("Default"); err != nil {
			return fmt.Errorf("failed switching to Default: %w", err)
		}
		fmt.Println("Fallback switched to: Default")
	}

	return os.Remove(path)
}

func InitDefaultConfig() (string, error) {
	if err := ensureDirs(); err != nil {
		return "", err
	}

	defPath := filepath.Join(ConfigsDir(), "Default.yaml")

	if _, err := os.Stat(defPath); err == nil {
		_ = os.WriteFile(CurrentLabelFile(), []byte("Default"), 0644)
		return defPath, os.ErrExist
	}

	if err := SaveYAML(DefaultConfig(), defPath); err != nil {
		return "", err
	}

	_ = os.WriteFile(CurrentLabelFile(), []byte("Default"), 0644)
	return defPath, nil
}
