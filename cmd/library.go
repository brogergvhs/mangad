package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/service"

	"github.com/spf13/cobra"
)

var (
	flagLibraryDB              string
	flagLibraryAddURL          string
	flagLibraryAddTitle        string
	flagLibraryAddOutput       string
	flagLibraryAddUnmonitored  bool
	flagLibraryRefreshInterval string
	flagLibraryRefreshAfterAdd bool
	flagLibraryRemoveForce     bool
	flagLibraryDownloadChapter string
	flagLibraryDownloadMissing bool
	flagLibraryRefreshScan     bool
)

var libraryCmd = &cobra.Command{Use: "lib", Short: "Manage tracked manga titles"}

var libraryAddCmd = &cobra.Command{Use: "add", Short: "Track a manga source URL", RunE: runLibraryAdd}

var libraryListCmd = &cobra.Command{Use: "list", Short: "List tracked manga titles", RunE: runLibraryList}

var libraryRefreshCmd = &cobra.Command{
	Use:   "rf [title_id]",
	Short: "Refresh discovered chapters for one or all tracked titles",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runLibraryRefresh,
}

var libraryScanCmd = &cobra.Command{
	Use:   "scan [title_id]",
	Short: "Verify completed download files still exist",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runLibraryScan,
}

var libraryMissingCmd = &cobra.Command{
	Use:   "missing <title_id>",
	Short: "List missing chapters for a tracked title",
	Args:  cobra.ExactArgs(1),
	RunE:  runLibraryMissing,
}

var libraryRemoveCmd = &cobra.Command{
	Use:   "rm <title_id>",
	Short: "Remove a tracked manga title",
	Args:  cobra.ExactArgs(1),
	RunE:  runLibraryRemove,
}

var libraryDownloadCmd = &cobra.Command{
	Use:   "dl [title_id]",
	Short: "Download library chapters",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runLibraryDownload,
}

func init() {
	libraryCmd.PersistentFlags().StringVar(&flagLibraryDB, "db", "", "path to MangaD SQLite database")
	libraryAddCmd.Flags().StringVar(&flagLibraryAddURL, "url", "", "manga series page URL")
	libraryAddCmd.Flags().StringVar(&flagLibraryAddTitle, "title", "", "display title")
	libraryAddCmd.Flags().StringVar(&flagLibraryAddOutput, "output", "", "output folder override")
	libraryAddCmd.Flags().BoolVar(&flagLibraryAddUnmonitored, "unmonitored", false, "add title without automatic monitoring")
	libraryAddCmd.Flags().StringVar(&flagLibraryRefreshInterval, "refresh-interval", "24h", "refresh interval label, e.g. 24h")
	libraryAddCmd.Flags().BoolVar(&flagLibraryRefreshAfterAdd, "refresh", false, "refresh chapters immediately after adding")
	libraryRefreshCmd.Flags().BoolVar(&flagLibraryRefreshScan, "scan", false, "verify downloaded files after refresh")
	libraryRemoveCmd.Flags().BoolVarP(&flagLibraryRemoveForce, "force", "f", false, "remove without confirmation")
	libraryDownloadCmd.Flags().StringVar(&flagLibraryDownloadChapter, "chapter", "", "download one chapter label instead of all missing")
	libraryDownloadCmd.Flags().BoolVar(&flagLibraryDownloadMissing, "missing", false, "download missing chapters")

	libraryCmd.AddCommand(libraryAddCmd, libraryListCmd, libraryRefreshCmd, libraryMissingCmd, libraryRemoveCmd, libraryDownloadCmd, libraryScanCmd)
	rootCmd.AddCommand(libraryCmd)
}

func runLibraryAdd(_ *cobra.Command, _ []string) error {
	return withLibrary(func(ctx context.Context, lib *service.LibraryService) error {
		title, err := lib.AddTitle(ctx, library.AddTitleParams{
			SourceURL:       flagLibraryAddURL,
			DisplayTitle:    flagLibraryAddTitle,
			OutputPath:      flagLibraryAddOutput,
			Monitored:       !flagLibraryAddUnmonitored,
			RefreshInterval: flagLibraryRefreshInterval,
		})
		if err != nil {
			return err
		}

		fmt.Printf("Tracked title %d: %s\nSource: %s\n", title.ID, title.DisplayTitle, title.SourceURL)
		if !flagLibraryRefreshAfterAdd {
			return nil
		}

		cfg, logSvc, err := runtimeConfig()
		if err != nil {
			return err
		}
		cfg.CookieDBPath = flagLibraryDB
		result, err := lib.RefreshTitle(ctx, cfg, logSvc, title)
		if err != nil {
			return err
		}
		printRefresh(result)
		return nil
	})
}

func runLibraryList(_ *cobra.Command, _ []string) error {
	return withLibrary(func(ctx context.Context, lib *service.LibraryService) error {
		titles, err := lib.ListTitles(ctx)
		if err != nil {
			return err
		}
		if len(titles) == 0 {
			fmt.Println("No tracked titles.")
			return nil
		}
		for _, title := range titles {
			printTitle(title)
		}
		return nil
	})
}

func runLibraryRefresh(_ *cobra.Command, args []string) error {
	return withLibrary(func(ctx context.Context, lib *service.LibraryService) error {
		cfg, logSvc, err := runtimeConfig()
		if err != nil {
			return err
		}
		cfg.CookieDBPath = flagLibraryDB
		if len(args) == 1 {
			id, err := titleID(args[0])
			if err != nil {
				return err
			}
			title, err := lib.GetTitle(ctx, id)
			if err != nil {
				return err
			}
			result, err := lib.RefreshTitle(ctx, cfg, logSvc, title)
			if err != nil {
				return err
			}
			printRefresh(result)
			if flagLibraryRefreshScan {
				scan, err := lib.ScanDownloads(ctx, id)
				if err != nil {
					return err
				}
				printScan(scan)
			}
			return nil
		}

		results, err := lib.RefreshMonitored(ctx, cfg, logSvc)
		for _, result := range results {
			printRefresh(result)
		}
		if err != nil {
			return err
		}
		if flagLibraryRefreshScan {
			scan, err := lib.ScanDownloads(ctx, 0)
			if err != nil {
				return err
			}
			printScan(scan)
		}
		return nil
	})
}

func runLibraryMissing(_ *cobra.Command, args []string) error {
	return withLibrary(func(ctx context.Context, lib *service.LibraryService) error {
		id, err := titleID(args[0])
		if err != nil {
			return err
		}
		title, missing, err := lib.MissingChapters(ctx, id)
		if err != nil {
			return err
		}

		fmt.Printf("Missing chapters for %s (%d):\n", title.DisplayTitle, len(missing))
		for _, chapter := range missing {
			fmt.Printf("%8s  %s\n", chapter.Label, chapter.URL)
		}
		return nil
	})
}

func runLibraryRemove(_ *cobra.Command, args []string) error {
	return withLibrary(func(ctx context.Context, lib *service.LibraryService) error {
		id, err := titleID(args[0])
		if err != nil {
			return err
		}
		title, err := lib.GetTitle(ctx, id)
		if err != nil {
			return err
		}
		if !flagLibraryRemoveForce {
			ok, err := confirmRemoval(title)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Println("Remove cancelled.")
				return nil
			}
		}

		title, err = lib.RemoveTitle(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("Removed title %d: %s\n", title.ID, title.DisplayTitle)
		return nil
	})
}

func runLibraryDownload(_ *cobra.Command, args []string) error {
	return withLibrary(func(ctx context.Context, lib *service.LibraryService) error {
		cfg, logSvc, err := runtimeConfig()
		if err != nil {
			return err
		}
		cfg.CookieDBPath = flagLibraryDB

		if len(args) == 0 {
			if flagLibraryDownloadChapter != "" {
				return fmt.Errorf("--chapter requires a title_id")
			}
			if !flagLibraryDownloadMissing {
				return fmt.Errorf("missing title_id; use --missing to download missing chapters for all monitored titles")
			}
			results, err := lib.DownloadMonitoredMissing(ctx, cfg, logSvc)
			return printDownloads(results, err)
		}

		id, err := titleID(args[0])
		if err != nil {
			return err
		}
		if flagLibraryDownloadChapter != "" {
			result, err := lib.DownloadChapterLabel(ctx, cfg, logSvc, id, flagLibraryDownloadChapter)
			if err != nil {
				return err
			}
			printDownload(result)
			return nil
		}

		results, err := lib.DownloadMissing(ctx, cfg, logSvc, id)
		return printDownloads(results, err)
	})
}

func runLibraryScan(_ *cobra.Command, args []string) error {
	return withLibrary(func(ctx context.Context, lib *service.LibraryService) error {
		var id int64
		var err error
		if len(args) == 1 {
			id, err = titleID(args[0])
			if err != nil {
				return err
			}
		}
		result, err := lib.ScanDownloads(ctx, id)
		if err != nil {
			return err
		}
		printScan(result)
		return nil
	})
}

func withLibrary(fn func(context.Context, *service.LibraryService) error) error {
	ctx := context.Background()
	lib, closeDB, err := service.OpenLibrary(ctx, flagLibraryDB)
	if err != nil {
		return err
	}
	defer closeDB()
	return fn(ctx, lib)
}

func titleID(value string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid title id %q", value)
	}
	return id, nil
}

func printTitle(title library.Title) {
	monitored := "no"
	if title.Monitored {
		monitored = "yes"
	}
	fmt.Printf("%3d  %s\n", title.ID, title.DisplayTitle)
	fmt.Printf("     source:     %s\n", title.SourceURL)
	fmt.Printf("     monitored:  %s\n", monitored)
	fmt.Printf("     chapters:   %d discovered, %d missing, %d completed\n", title.DiscoveredCount, title.MissingCount, title.CompletedCount)
	fmt.Printf("     refreshed:  %s\n", formatTime(title.LastRefreshedAt))
}

func printRefresh(result service.RefreshResult) {
	fmt.Printf("Refreshed %s: %d chapters discovered\n", result.Title.DisplayTitle, result.Count)
}

func printDownload(result service.ChapterDownloadResult) {
	fmt.Printf("Downloaded chapter %s: %s (%d pages)\n", result.Chapter.Label, result.OutputFile, result.Images)
}

func printDownloads(results []service.ChapterDownloadResult, err error) error {
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("No missing chapters.")
	}
	for _, result := range results {
		printDownload(result)
	}
	return nil
}

func printScan(result service.ScanResult) {
	fmt.Printf("Scanned downloads: %d checked, %d missing\n", result.Checked, result.Missing)
}

func formatTime(value *time.Time) string {
	if value == nil {
		return "never"
	}
	return value.Local().Format(time.RFC3339)
}

func confirmRemoval(title library.Title) (bool, error) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("Remove title %d (%s)? This deletes tracked chapters/download state, not files. [y/N]: ", title.ID, title.DisplayTitle)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes", nil
}
