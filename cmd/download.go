package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/brogergvhs/kaodoku/internal/chapters"
	"github.com/brogergvhs/kaodoku/internal/config"
	"github.com/brogergvhs/kaodoku/internal/service"
	"github.com/brogergvhs/kaodoku/internal/ui"
	"github.com/brogergvhs/kaodoku/internal/util"

	"github.com/spf13/cobra"
)

var (
	// selection
	flagURL          string
	flagChapter      string
	flagRange        string
	flagExcludeRange string
	flagList         string
	flagExcludeList  string
	flagAllowExt     string

	// runtime
	flagOutput         string
	flagImageWorkers   int
	flagChapterWorkers int
	flagKeepFolders    bool
	flagDryRun         bool
	flagSkipBroken     bool
	flagCheckJS        bool

	// headers/auth
	flagCookie     string
	flagCookieFile string
	flagUserAgent  string
)

func init() {
	downloadCmd := &cobra.Command{
		Use:   "download",
		Short: "Download manga chapters and produce CBZ files. Uses the defaults from the selected config, overwritten by CLI flags",
		RunE:  runDownload,
	}

	// selection
	downloadCmd.Flags().StringVar(&flagURL, "url", "", "manga series/chapters page URL")
	downloadCmd.Flags().StringVar(&flagChapter, "chapter", "", "download single chapter by index or label (e.g. 5 or 28.5)")
	downloadCmd.Flags().StringVar(&flagRange, "range", "", "download range of chapters by index (e.g. 5-12)")
	downloadCmd.Flags().StringVar(&flagExcludeRange, "exclude-range", "", "exclude range of chapters by index (e.g. 5-12)")
	downloadCmd.Flags().StringVar(&flagList, "list", "", "download specific chapter indices (e.g. 1,3,5)")
	downloadCmd.Flags().StringVar(&flagExcludeList, "exclude-list", "", "exclude specific chapter indices (e.g. 1,3,5)")
	downloadCmd.Flags().StringVar(&flagAllowExt, "allow-ext", "", "Allowed image extensions (e.g. \"webp|jpg|png\")")

	// runtime
	downloadCmd.Flags().StringVar(&flagOutput, "output", "", "output folder for CBZ files")
	downloadCmd.Flags().IntVar(&flagImageWorkers, "image-workers", 5, "parallel image downloads per chapter")
	downloadCmd.Flags().IntVar(&flagChapterWorkers, "chapter-workers", 2, "parallel chapter downloads")
	downloadCmd.Flags().BoolVar(&flagKeepFolders, "keep-folders", false, "keep temporary folders")
	downloadCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "show what would be downloaded, don’t download")
	downloadCmd.Flags().BoolVar(&flagSkipBroken, "skip-broken", false, "skip failed images instead of failing the whole chapter")
	downloadCmd.Flags().BoolVar(&flagCheckJS, "check-js", false, "Enable generic JS scanning & dynamic AJAX endpoint discovery")

	// headers/auth
	downloadCmd.Flags().StringVar(&flagCookie, "cookie", "", "cookie string, e.g. \"key=value; other=123\"")
	downloadCmd.Flags().StringVar(&flagCookieFile, "cookie-file", "", "path to a text file with cookies (one header line)")
	downloadCmd.Flags().StringVar(&flagUserAgent, "user-agent", "", "override User-Agent")

	rootCmd.AddCommand(downloadCmd)
}

func runDownload(cmd *cobra.Command, _ []string) error {
	cfg, logSvc, usedPath, err := prepareConfigAndLogger(cmd)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scraperName := "generic"
	var sourceID string
	if src, ok := service.ResolveSourceForURL(ctx, cfg.DefaultURL, "", logSvc); ok {
		applied := service.ConfigForSource(*cfg, src, service.SourceConfigOptions{
			PreserveAllowedExtensions: cmd.Flags().Changed("allow-ext"),
		})
		cfg = &applied
		scraperName = src.Scraper
		sourceID = src.ID
	}

	printLoadedConfig(usedPath, cfg)
	if sourceID != "" {
		fmt.Printf("Using source: %s (scraper=%s)\n\n", sourceID, scraperName)
	}

	downloadSvc, err := service.NewSourceDownloadService(cfg, logSvc, nil, scraperName)
	if err != nil {
		return err
	}

	allChapters, _, _, err := downloadSvc.FetchChapters(ctx, cfg.DefaultURL)
	if err != nil {
		return err
	}

	if shouldPrintChapterCount(cfg) {
		fmt.Printf("Found %d chapters on the site.\n\n", len(allChapters))
	}

	selected, err := downloadSvc.SelectChapters(allChapters, selectionFromConfig(cfg))
	if err != nil {
		return err
	}

	if flagDryRun {
		return doDryRun(ctx, downloadSvc, selected)
	}

	downloadSvc.SetProgressManager(service.NewTerminalProgressManager())
	summary, err := downloadSvc.Download(ctx, selected)
	if ctx.Err() != nil {
		// Workers have stopped; now removing partial temp folders is safe.
		fmt.Println("\nInterrupted, cleaning up...")
		util.CleanupUnfinishedTempFolders(cfg.Output)
		util.RemoveIfEmpty(cfg.Output)
		return fmt.Errorf("download interrupted")
	}
	if err != nil {
		return err
	}

	printDownloadSummary(summary)
	return nil
}

func prepareConfigAndLogger(cmd *cobra.Command) (*config.Config, ui.Log, string, error) {
	cfg, usedPath, err := config.LoadMerged(config.Options{
		IgnoreConfig:        flagIgnoreConfig,
		Debug:               flagDebug,
		Output:              flagOutput,
		ImageWorkers:        0,
		ChapterWorkers:      0,
		KeepFolders:         flagKeepFolders,
		DefaultURL:          flagURL,
		DefaultRange:        flagRange,
		DefaultExcludeRange: flagExcludeRange,
		DefaultList:         flagList,
		DefaultExcludeList:  flagExcludeList,
		CheckJS:             flagCheckJS,
		Cookie:              flagCookie,
		CookieFile:          flagCookieFile,
		UserAgent:           flagUserAgent,
		SkipBroken:          flagSkipBroken,
	})
	if err != nil {
		return nil, nil, "", err
	}

	if cmd.Flags().Changed("image-workers") {
		cfg.ImageWorkers = flagImageWorkers
	}
	if cmd.Flags().Changed("chapter-workers") {
		cfg.ChapterWorkers = flagChapterWorkers
	}
	// Explicit boolean flags override the config file in both directions;
	// the Options merge can only turn them on.
	if cmd.Flags().Changed("keep-folders") {
		cfg.KeepFolders = flagKeepFolders
	}
	if cmd.Flags().Changed("skip-broken") {
		cfg.SkipBroken = flagSkipBroken
	}
	if cmd.Flags().Changed("check-js") {
		cfg.CheckJS = flagCheckJS
	}
	if cmd.Flags().Changed("debug") {
		cfg.Debug = flagDebug
	}
	if flagAllowExt != "" {
		cfg.AllowExt = splitExt(flagAllowExt)
	}

	logSvc := ui.NewLogger(cfg.Debug)

	if cfg.Output == "" {
		cfg.Output = "."
	}
	if err := os.MkdirAll(cfg.Output, 0755); err != nil {
		return nil, nil, "", fmt.Errorf("cannot create output folder: %w", err)
	}

	if cfg.DefaultURL == "" {
		return nil, nil, "", fmt.Errorf("missing --url and no default_url in config")
	}

	return cfg, logSvc, usedPath, nil
}

func printLoadedConfig(usedPath string, cfg *config.Config) {
	if usedPath != "" {
		fmt.Printf("Config file: %s\n", usedPath)
	}

	fmt.Println("Full config:")
	cfg.Print()
	fmt.Println()
}

func doDryRun(ctx context.Context, downloadSvc *service.DownloadService, selected []chapters.Chapter) error {
	fmt.Printf("Dry-run: %d chapters selected.\n\n", len(selected))
	for i, ch := range selected {
		fmt.Printf("%3d) %s  [%s]\n    %s\n", i+1, ch.Title, ch.Label, ch.URL)
	}

	if len(selected) == 1 {
		ch := selected[0]
		fmt.Printf("\nFetching images for chapter %s (%s)...\n\n", ch.Title, ch.Label)

		images, err := downloadSvc.FetchImages(ctx, ch)
		if err != nil {
			return fmt.Errorf("failed to fetch images for %s: %w", ch.Label, err)
		}

		if len(images) == 0 {
			fmt.Println("No images found.")
		} else {
			fmt.Printf("Found %d images:\n\n", len(images))
			for i, u := range images {
				fmt.Printf("%3d) %s\n", i+1, u)
			}
		}

		fmt.Println()
	}

	return nil
}

func printDownloadSummary(summary service.DownloadSummary) {
	fmt.Println()
	fmt.Println("Download Summary:")
	fmt.Printf("Chapters: %d\n", summary.Chapters)
	if summary.FailedChapters > 0 {
		fmt.Printf("Failed:   %d\n", summary.FailedChapters)
	}
	fmt.Printf("Images:   %d\n", summary.Images)
	fmt.Printf("Data:     %s\n", util.Human(summary.Bytes))
	fmt.Printf("Time:     %s\n", summary.Duration.Round(time.Second))
	if len(summary.Errors) > 0 {
		fmt.Println("Errors:")
		for _, msg := range summary.Errors {
			fmt.Printf(" - %s\n", msg)
		}
	}
	fmt.Println("\nAll done.")
}

func shouldPrintChapterCount(cfg *config.Config) bool {
	return flagChapter == "" && flagRange == "" && flagList == "" &&
		cfg.DefaultRange == "" && cfg.DefaultList == ""
}

func selectionFromConfig(cfg *config.Config) service.ChapterSelection {
	return service.ChapterSelection{
		Chapter:      flagChapter,
		Range:        firstNonEmpty(flagRange, cfg.DefaultRange),
		ExcludeRange: firstNonEmpty(flagExcludeRange, cfg.DefaultExcludeRange),
		List:         firstNonEmpty(flagList, cfg.DefaultList),
		ExcludeList:  firstNonEmpty(flagExcludeList, cfg.DefaultExcludeList),
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

func splitExt(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == '|' || r == ',' || r == ' '
	})

	out := []string{}
	for _, f := range fields {
		f = strings.ToLower(strings.TrimSpace(f))
		if f != "" {
			out = append(out, f)
		}
	}

	return out
}
