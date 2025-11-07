package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Output         string   `yaml:"output"`
	ImageWorkers   int      `yaml:"image_workers"`
	ChapterWorkers int      `yaml:"chapter_workers"`
	KeepFolders    bool     `yaml:"keep_folders"`
	Debug          bool     `yaml:"debug"`
	AllowExt       []string `yaml:"allow_ext"`

	DefaultURL          string `yaml:"default_url"`
	DefaultRange        string `yaml:"default_range"`
	DefaultExcludeRange string `yaml:"default_exclude_range"`
	DefaultList         string `yaml:"default_list"`
	DefaultExcludeList  string `yaml:"default_exclude_list"`

	Cookie     string `yaml:"cookie"`
	CookieFile string `yaml:"cookie_file"`
	UserAgent  string `yaml:"user_agent"`

	SkipBroken bool `yaml:"skip_broken"`
}

type Options struct {
	IgnoreConfig        bool
	Debug               bool
	Output              string
	ImageWorkers        int
	ChapterWorkers      int
	KeepFolders         bool
	DefaultURL          string
	DefaultRange        string
	DefaultExcludeRange string
	DefaultList         string
	DefaultExcludeList  string
	Cookie              string
	CookieFile          string
	UserAgent           string
	SkipBroken          bool
}

func DefaultConfig() *Config {
	return &Config{
		Output:              ".",
		ImageWorkers:        5,
		ChapterWorkers:      2,
		KeepFolders:         false,
		Debug:               false,
		DefaultURL:          "",
		DefaultRange:        "",
		DefaultExcludeRange: "",
		DefaultList:         "",
		DefaultExcludeList:  "",
		Cookie:              "",
		CookieFile:          "",
		UserAgent:           "",
		SkipBroken:          false,
		AllowExt:            []string{"jpg", "jpeg", "png", "webp"},
	}
}

func SaveYAML(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func loadYAML(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}

	return &c, nil
}

func LoadMerged(opts Options) (*Config, string, error) {
	if opts.IgnoreConfig {
		cfg := DefaultConfig()
		mergeConfig(cfg, opts)
		normalizeDefaults(cfg)
		return cfg, "(ignored config)", nil
	}

	activePath, err := ActiveConfigPath()
	if err == ErrNoConfig || activePath == "" {
		cfg := DefaultConfig()
		mergeConfig(cfg, opts)
		normalizeDefaults(cfg)
		return cfg, "(default config in memory)\nRun `mangad config init` to create an actual config\n", nil
	}
	if err != nil {
		return nil, "", err
	}

	cfg, err := loadYAML(activePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load config %s: %w", activePath, err)
	}

	mergeConfig(cfg, opts)
	normalizeDefaults(cfg)

	return cfg, activePath, nil
}

func mergeConfig(c *Config, o Options) {
	if o.Output != "" {
		c.Output = o.Output
	}
	if o.ImageWorkers != 0 {
		c.ImageWorkers = o.ImageWorkers
	}
	if o.ChapterWorkers != 0 {
		c.ChapterWorkers = o.ChapterWorkers
	}
	if o.KeepFolders {
		c.KeepFolders = true
	}
	if o.Debug {
		c.Debug = true
	}
	if o.DefaultURL != "" {
		c.DefaultURL = o.DefaultURL
	}
	if o.DefaultRange != "" {
		c.DefaultRange = o.DefaultRange
	}
	if o.DefaultExcludeRange != "" {
		c.DefaultExcludeRange = o.DefaultExcludeRange
	}
	if o.DefaultList != "" {
		c.DefaultList = o.DefaultList
	}
	if o.DefaultExcludeList != "" {
		c.DefaultExcludeList = o.DefaultExcludeList
	}
	if o.Cookie != "" {
		c.Cookie = o.Cookie
	}
	if o.CookieFile != "" {
		c.CookieFile = o.CookieFile
	}
	if o.UserAgent != "" {
		c.UserAgent = o.UserAgent
	}
	if o.SkipBroken {
		c.SkipBroken = true
	}
}

func normalizeDefaults(c *Config) {
	if c.Output == "" {
		c.Output = "."
	}
	if c.ImageWorkers == 0 {
		c.ImageWorkers = 5
	}
	if c.ChapterWorkers == 0 {
		c.ChapterWorkers = 2
	}
}

func (c *Config) Print() {
	if c.Output != "" {
		fmt.Printf(" -output: %s\n", c.Output)
	}
	fmt.Printf(" -image_workers: %d\n", c.ImageWorkers)
	fmt.Printf(" -chapter_workers: %d\n", c.ChapterWorkers)
	if c.KeepFolders {
		fmt.Printf(" -keep_folders: %t\n", c.KeepFolders)
	}
	if c.Debug {
		fmt.Printf(" -debug: %t\n", c.Debug)
	}
	if c.DefaultURL != "" {
		fmt.Printf(" -url: %s\n", c.DefaultURL)
	}
	if c.DefaultRange != "" {
		fmt.Printf(" -range: %s\n", c.DefaultRange)
	}
	if c.DefaultExcludeRange != "" {
		fmt.Printf(" -exclude_range: %s\n", c.DefaultExcludeRange)
	}
	if c.DefaultList != "" {
		fmt.Printf(" -list: %s\n", c.DefaultList)
	}
	if c.DefaultExcludeList != "" {
		fmt.Printf(" -exclude_list: %s\n", c.DefaultExcludeList)
	}
	if c.CookieFile != "" {
		fmt.Printf(" -cookie_file: %s\n", c.CookieFile)
	}
	if c.SkipBroken {
		fmt.Printf(" -skip_broken: %t\n", c.SkipBroken)
	}
	if len(c.AllowExt) > 0 {
		fmt.Printf(" -allow_ext: %s\n", strings.Join(c.AllowExt, ", "))
	}
}
