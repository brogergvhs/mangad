package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/downloader"
	"github.com/brogergvhs/mangad/internal/providers/generic"
	"github.com/brogergvhs/mangad/internal/ui"
	"github.com/brogergvhs/mangad/internal/util"

	cloudflarebp "github.com/DaRealFreak/cloudflare-bp-go"
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
	flagWithCF         bool

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
	downloadCmd.Flags().BoolVar(&flagWithCF, "with-cf", false, "Allow using embedded Selenium fallback when Cloudflare blocks requests. Requires a working 'python3' executable with SeleniumBase installed.")

	// headers/auth
	downloadCmd.Flags().StringVar(&flagCookie, "cookie", "", "cookie string, e.g. \"key=value; other=123\"")
	downloadCmd.Flags().StringVar(&flagCookieFile, "cookie-file", "", "path to a text file with cookies (one header line)")
	downloadCmd.Flags().StringVar(&flagUserAgent, "user-agent", "", "override User-Agent")

	rootCmd.AddCommand(downloadCmd)
}

func runDownload(cmd *cobra.Command, _ []string) error {
	cfg, logSvc, err := prepareConfigAndLogger(cmd)
	if err != nil {
		return err
	}

	client, scr, ctx, err := setupEnvironment(cfg, logSvc)
	if err != nil {
		return err
	}

	allChapters, err := fetchAllChapters(ctx, scr, cfg)
	if err != nil {
		return err
	}

	selected, err := selectChapters(allChapters, cfg)
	if err != nil {
		return err
	}

	if len(selected) == 0 {
		return fmt.Errorf("no chapters selected")
	}

	if flagDryRun {
		return doDryRun(ctx, scr, selected)
	}

	return performDownloads(ctx, scr, client, cfg, logSvc, selected)
}

func prepareConfigAndLogger(cmd *cobra.Command) (*config.Config, *ui.Logger, error) {
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
		WithCF:              flagWithCF,
		Cookie:              flagCookie,
		CookieFile:          flagCookieFile,
		UserAgent:           flagUserAgent,
		SkipBroken:          flagSkipBroken,
	})
	if err != nil {
		return nil, nil, err
	}

	if cmd.Flags().Changed("image-workers") {
		cfg.ImageWorkers = flagImageWorkers
	}
	if cmd.Flags().Changed("chapter-workers") {
		cfg.ChapterWorkers = flagChapterWorkers
	}
	if flagAllowExt != "" {
		cfg.AllowExt = splitExt(flagAllowExt)
	}

	logSvc := ui.NewLogger(cfg.Debug)

	if usedPath != "" {
		fmt.Printf("Config file: %s\n", usedPath)
	}

	if cfg.Output == "" {
		cfg.Output = "."
	}
	if err := os.MkdirAll(cfg.Output, 0755); err != nil {
		return nil, nil, fmt.Errorf("cannot create output folder: %w", err)
	}

	fmt.Println("Full config:")
	cfg.Print()
	fmt.Println()

	if cfg.DefaultURL == "" {
		return nil, nil, fmt.Errorf("missing --url and no default_url in config")
	}

	return cfg, logSvc, nil
}

func setupEnvironment(cfg *config.Config, logSvc *ui.Logger) (*http.Client, *generic.Scraper, context.Context, error) {
	client, err := util.NewHTTPClient(util.HTTPClientOptions{
		Timeout:     30 * time.Second,
		UserAgent:   util.PickUserAgent(cfg.UserAgent),
		Cookie:      cfg.Cookie,
		Transport:   cloudflarebp.AddCloudFlareByPass(http.DefaultTransport),
		CookieFile:  cfg.CookieFile,
		DebugLogger: logSvc,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	ctx := context.Background()
	util.SetupInterruptHandler(cfg.Output)
	scr := generic.NewScraper(client, logSvc, cfg.AllowExt, cfg.CheckJS, cfg.WithCF)

	return client, scr, ctx, nil
}

func fetchAllChapters(ctx context.Context, scr *generic.Scraper, cfg *config.Config) ([]chapters.Chapter, error) {
	allChaptersRaw, err := scr.GetChapters(ctx, cfg.DefaultURL)
	if err != nil {
		return nil, err
	}

	allChapters := make([]chapters.Chapter, len(allChaptersRaw))
	for i, c := range allChaptersRaw {
		allChapters[i] = chapters.Chapter{Chapter: c}
	}

	if flagChapter == "" && flagRange == "" && flagList == "" &&
		cfg.DefaultRange == "" && cfg.DefaultList == "" {
		fmt.Printf("Found %d chapters on the site.\n\n", len(allChapters))
	}

	return allChapters, nil
}

func selectChapters(all []chapters.Chapter, cfg *config.Config) ([]chapters.Chapter, error) {
	finalRange := firstNonEmpty(flagRange, cfg.DefaultRange)
	finalExcludeRange := firstNonEmpty(flagExcludeRange, cfg.DefaultExcludeRange)
	finalList := firstNonEmpty(flagList, cfg.DefaultList)
	finalExcludeList := firstNonEmpty(flagExcludeList, cfg.DefaultExcludeList)

	if flagChapter != "" {
		direct := chapters.FilterChaptersByLabel(all, flagChapter)
		if len(direct) > 0 {
			return direct, nil
		}

		var idx int
		if _, err := fmt.Sscanf(flagChapter, "%d", &idx); err == nil && idx > 0 {
			return chapters.Filter(all, strconv.Itoa(idx), finalRange, finalExcludeRange, finalList, finalExcludeList)
		}

		return nil, fmt.Errorf("chapter '%s' not found", flagChapter)
	}

	return chapters.Filter(all, "", finalRange, finalExcludeRange, finalList, finalExcludeList)
}

func doDryRun(ctx context.Context, scr *generic.Scraper, selected []chapters.Chapter) error {
	fmt.Printf("Dry-run: %d chapters selected.\n\n", len(selected))
	for i, ch := range selected {
		fmt.Printf("%3d) %s  [%s]\n    %s\n", i+1, ch.Title, ch.Label, ch.URL)
	}

	if len(selected) == 1 {
		ch := selected[0]
		fmt.Printf("\nFetching images for chapter %s (%s)...\n\n", ch.Title, ch.Label)

		images, err := scr.GetImages(ctx, ch.URL)
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

func performDownloads(ctx context.Context, scr *generic.Scraper, client *http.Client, cfg *config.Config, logSvc *ui.Logger, selected []chapters.Chapter) error {
	pm := ui.NewProgressManager(cfg.ChapterWorkers)
	defer pm.Close()

	stats := &ui.Stats{}
	dl := downloader.New(client, cfg.Debug, cfg.Output, cfg.SkipBroken)
	start := time.Now()

	sem := make(chan struct{}, max(1, cfg.ChapterWorkers))
	var wg sync.WaitGroup

	for _, ch := range selected {
		wg.Add(1)
		sem <- struct{}{}

		go func(ch chapters.Chapter) {
			defer wg.Done()
			defer func() { <-sem }()

			images, err := scr.GetImages(ctx, ch.URL)
			if err != nil || len(images) == 0 {
				logSvc.Errorf("No images for %s (%s): %v", ch.Title, ch.Label, err)
				return
			}

			handle := pm.Register("Ch." + ch.Label)
			handle.SetTotal(len(images))

			tmpFolder := filepath.Join(cfg.Output, ch.FolderName())

			cbzOut := filepath.Join(cfg.Output, ch.OutputCBZ())

			files, bytes, err := dl.DownloadImagesConcurrently(ctx, images, tmpFolder, ch.URL, max(1, cfg.ImageWorkers), handle)
			if err != nil {
				logSvc.Errorf("Chapter %s failed: %v", ch.Label, err)

				return
			}

			if err := util.CreateCBZ(files, cbzOut); err != nil {
				logSvc.Errorf("CBZ for %s failed: %v", ch.Label, err)

				return
			}

			if !cfg.KeepFolders {
				util.CleanupFolder(tmpFolder)
			}

			handle.MarkDone()
			stats.TotalChapters.Add(1)
			stats.TotalImages.Add(int64(len(files)))
			stats.TotalBytes.Add(bytes)
		}(ch)
	}
	wg.Wait()
	pm.Close()

	fmt.Println()
	fmt.Println("Download Summary:")
	fmt.Printf("Chapters: %d\n", stats.TotalChapters.Load())
	fmt.Printf("Images:   %d\n", stats.TotalImages.Load())
	fmt.Printf("Data:     %s\n", util.Human(stats.TotalBytes.Load()))
	fmt.Printf("Time:     %s\n", time.Since(start).Round(time.Second))
	fmt.Println("\nAll done.")

	return nil
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
