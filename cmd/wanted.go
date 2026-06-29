package cmd

import (
	"context"
	"fmt"
	"strconv"

	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/service"

	"github.com/spf13/cobra"
)

var (
	flagWantedDB              string
	flagWantedLimit           int
	flagWantedOutput          string
	flagWantedUnmonitored     bool
	flagWantedRefreshInterval string
)

var wantedCmd = &cobra.Command{Use: "wanted", Short: "Manage AniList-backed wanted manga"}

var wantedSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search AniList manga",
	Args:  cobra.ExactArgs(1),
	RunE:  runWantedSearch,
}

var wantedAddCmd = &cobra.Command{
	Use:   "add <anilist_id>",
	Short: "Add an AniList manga as wanted",
	Args:  cobra.ExactArgs(1),
	RunE:  runWantedAdd,
}

var wantedListCmd = &cobra.Command{Use: "list", Short: "List wanted manga", RunE: runWantedList}

var wantedMatchCmd = &cobra.Command{
	Use:   "match <catalog_id>",
	Short: "Find source matches for a wanted manga",
	Args:  cobra.ExactArgs(1),
	RunE:  runWantedMatch,
}

var wantedMatchesCmd = &cobra.Command{
	Use:   "matches <catalog_id>",
	Short: "List source matches for a wanted manga",
	Args:  cobra.ExactArgs(1),
	RunE:  runWantedMatches,
}

var wantedTrackCmd = &cobra.Command{
	Use:   "track <match_id>",
	Short: "Track a selected source match in the library",
	Args:  cobra.ExactArgs(1),
	RunE:  runWantedTrack,
}

func init() {
	wantedCmd.PersistentFlags().StringVar(&flagWantedDB, "db", "", "path to MangaD SQLite database")
	wantedSearchCmd.Flags().IntVar(&flagWantedLimit, "limit", 10, "maximum AniList results")
	wantedTrackCmd.Flags().StringVar(&flagWantedOutput, "output", "", "output folder override")
	wantedTrackCmd.Flags().BoolVar(&flagWantedUnmonitored, "unmonitored", false, "add title without automatic monitoring")
	wantedTrackCmd.Flags().StringVar(&flagWantedRefreshInterval, "refresh-interval", "24h", "refresh interval label")
	wantedCmd.AddCommand(wantedSearchCmd, wantedAddCmd, wantedListCmd, wantedMatchCmd, wantedMatchesCmd, wantedTrackCmd)
	rootCmd.AddCommand(wantedCmd)
}

func runWantedSearch(_ *cobra.Command, args []string) error {
	return withWanted(func(ctx context.Context, svc *service.WantedService) error {
		items, err := svc.SearchAniList(ctx, args[0], flagWantedLimit)
		if err != nil {
			return err
		}
		for _, item := range items {
			printWantedManga(item)
		}
		return nil
	})
}

func runWantedAdd(_ *cobra.Command, args []string) error {
	id, err := strconv.Atoi(args[0])
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid AniList id %q", args[0])
	}
	return withWanted(func(ctx context.Context, svc *service.WantedService) error {
		item, err := svc.AddAniListWanted(ctx, id)
		if err != nil {
			return err
		}
		fmt.Printf("Wanted %d: %s (AniList %s)\n", item.ID, wantedTitle(item), item.ProviderID)
		return nil
	})
}

func runWantedList(_ *cobra.Command, _ []string) error {
	return withWanted(func(ctx context.Context, svc *service.WantedService) error {
		items, err := svc.ListWanted(ctx)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			fmt.Println("No wanted manga.")
			return nil
		}
		for _, item := range items {
			printWantedManga(item)
		}
		return nil
	})
}

func runWantedMatch(_ *cobra.Command, args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid catalog id %q", args[0])
	}
	return withWanted(func(ctx context.Context, svc *service.WantedService) error {
		cfg, logSvc, err := runtimeConfig()
		if err != nil {
			return err
		}
		cfg.CookieDBPath = flagWantedDB
		matches, err := svc.MatchSources(ctx, cfg, logSvc, id)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			fmt.Println("No source matches found.")
			return nil
		}
		for _, match := range matches {
			printWantedMatch(match)
		}
		return nil
	})
}

func runWantedMatches(_ *cobra.Command, args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid catalog id %q", args[0])
	}
	return withWanted(func(ctx context.Context, svc *service.WantedService) error {
		matches, err := svc.ListMatches(ctx, id)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			fmt.Println("No source matches.")
			return nil
		}
		for _, match := range matches {
			printWantedMatch(match)
		}
		return nil
	})
}

func runWantedTrack(_ *cobra.Command, args []string) error {
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return fmt.Errorf("invalid match id %q", args[0])
	}
	return withWanted(func(ctx context.Context, svc *service.WantedService) error {
		title, err := svc.TrackMatch(ctx, id, flagWantedOutput, !flagWantedUnmonitored, flagWantedRefreshInterval)
		if err != nil {
			return err
		}
		fmt.Printf("Tracked title %d: %s\nSource: %s\n", title.ID, title.DisplayTitle, title.SourceURL)
		return nil
	})
}

func withWanted(fn func(context.Context, *service.WantedService) error) error {
	ctx := context.Background()
	svc, closeDB, err := service.OpenWanted(ctx, flagWantedDB)
	if err != nil {
		return err
	}
	defer closeDB()
	return fn(ctx, svc)
}

func printWantedManga(item catalog.Manga) {
	fmt.Printf("%3d  %s\n", item.ID, wantedTitle(item))
	fmt.Printf("     anilist: %s", item.ProviderID)
	if item.Chapters != nil {
		fmt.Printf(" chapters=%d", *item.Chapters)
	}
	if item.Wanted {
		fmt.Print(" wanted=yes")
	}
	fmt.Println()
}

func printWantedMatch(match catalog.Match) {
	fmt.Printf("%3d  %-14s chapters=%d confidence=%.2f\n", match.ID, match.SourceID, match.ChaptersFound, match.Confidence)
	fmt.Printf("     %s\n", match.SourceURL)
	if match.Error != "" {
		fmt.Printf("     error: %s\n", match.Error)
	}
}

func wantedTitle(item catalog.Manga) string {
	for _, value := range []string{item.TitleEnglish, item.TitleRomaji, item.TitleNative} {
		if value != "" {
			return value
		}
	}
	return item.Provider + ":" + item.ProviderID
}
