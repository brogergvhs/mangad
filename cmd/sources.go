package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/brogergvhs/mangad/internal/service"
	"github.com/brogergvhs/mangad/internal/sources"

	"github.com/spf13/cobra"
)

var (
	flagSourcesDB          string
	flagSourcesRegistryURL string
	flagSourcesOutput      string
)

var sourcesCmd = &cobra.Command{Use: "sources", Short: "Manage supported website profiles"}

var sourcesListCmd = &cobra.Command{Use: "list", Short: "List supported sources", RunE: runSourcesList}

var sourcesSyncCmd = &cobra.Command{Use: "sync", Short: "Sync built-in and registry sources", RunE: runSourcesSync}

var sourcesImportCmd = &cobra.Command{
	Use:   "import <profile.yaml|->",
	Short: "Import a local source profile into the DB",
	Args:  cobra.ExactArgs(1),
	RunE:  runSourcesImport,
}

var sourcesExportCmd = &cobra.Command{
	Use:   "export <source_id>",
	Short: "Export a DB source profile as YAML",
	Args:  cobra.ExactArgs(1),
	RunE:  runSourcesExport,
}

var sourcesRemoveCmd = &cobra.Command{
	Use:   "remove <source_id>",
	Short: "Remove a local DB source profile",
	Args:  cobra.ExactArgs(1),
	RunE:  runSourcesRemove,
}

var sourcesTemplateCmd = &cobra.Command{
	Use:   "template <source_id>",
	Short: "Print a starter source profile YAML",
	Args:  cobra.ExactArgs(1),
	RunE:  runSourcesTemplate,
}

var sourcesVerifyCmd = &cobra.Command{
	Use:   "verify <source_id>",
	Short: "Verify one source profile",
	Args:  cobra.ExactArgs(1),
	RunE:  runSourcesVerify,
}

func init() {
	sourcesCmd.PersistentFlags().StringVar(&flagSourcesDB, "db", "", "path to MangaD SQLite database")
	sourcesSyncCmd.Flags().StringVar(&flagSourcesRegistryURL, "registry-url", "", "remote JSON/YAML source registry URL")
	sourcesExportCmd.Flags().StringVarP(&flagSourcesOutput, "output", "o", "", "write exported YAML to a file instead of stdout")
	sourcesCmd.AddCommand(sourcesListCmd, sourcesSyncCmd, sourcesImportCmd, sourcesExportCmd, sourcesRemoveCmd, sourcesTemplateCmd, sourcesVerifyCmd)
	rootCmd.AddCommand(sourcesCmd)
}

func runSourcesList(_ *cobra.Command, _ []string) error {
	return withSourceJobs(func(ctx context.Context, svc *service.JobService) error {
		if err := svc.SyncSources(ctx, ""); err != nil {
			return err
		}
		all, err := svc.ListSources(ctx)
		if err != nil {
			return err
		}
		if len(all) == 0 {
			fmt.Println("No sources.")
			return nil
		}
		for _, src := range all {
			fmt.Printf("%-s  |  %-s  |  %-s  |  %s\n", src.ID, src.Origin, src.Status, src.Name)
			fmt.Printf("  base:       %s\n", src.BaseURL)
			fmt.Printf("  sample:     %s\n", src.SampleMangaURL)
			fmt.Printf("  extensions: %s\n", strings.Join(src.AllowedExtensions, ", "))
			fmt.Printf("  chapters:   %d found, min %d\n", src.ChaptersFound, src.MinChapters)
			fmt.Printf("  images:     %d found", src.SampleImagesFound)
			if len(src.ImageExtensions) > 0 {
				fmt.Printf(" (%s)", strings.Join(src.ImageExtensions, ", "))
			}
			fmt.Println()
			if src.LastCheckedAt != "" {
				fmt.Printf("  checked:    %s\n", src.LastCheckedAt)
			}
			if src.RequiresBrowserSolver {
				fmt.Println("  solver:     browser required")
			}
			if src.RequiresBrowserDownload {
				fmt.Println("  download:   browser downloader required")
			}
			if src.LastError != "" {
				fmt.Printf("  error:      %s\n", src.LastError)
			}
		}
		return nil
	})
}

func runSourcesSync(_ *cobra.Command, _ []string) error {
	return withSourceJobs(func(ctx context.Context, svc *service.JobService) error {
		if err := svc.SyncSources(ctx, flagSourcesRegistryURL); err != nil {
			return err
		}
		if flagSourcesRegistryURL != "" {
			if err := svc.SetSetting(ctx, service.SettingSourceRegistryURL, flagSourcesRegistryURL); err != nil {
				return err
			}
		}
		fmt.Println("Sources synced.")
		return nil
	})
}

func runSourcesImport(_ *cobra.Command, args []string) error {
	return withSourceJobs(func(ctx context.Context, svc *service.JobService) error {
		body, err := readSourceInput(args[0])
		if err != nil {
			return err
		}
		profile, err := sources.DecodeProfile(body)
		if err != nil {
			return err
		}
		if err := svc.ImportLocalSource(ctx, profile); err != nil {
			return err
		}
		fmt.Printf("Imported local source %s.\n", profile.ID)
		return nil
	})
}

func runSourcesExport(_ *cobra.Command, args []string) error {
	return withSourceJobs(func(ctx context.Context, svc *service.JobService) error {
		if err := svc.SyncSources(ctx, ""); err != nil {
			return err
		}
		body, err := svc.ExportSource(ctx, args[0])
		if err != nil {
			return err
		}
		if flagSourcesOutput == "" {
			fmt.Print(string(body))
			return nil
		}
		if err := os.WriteFile(flagSourcesOutput, body, 0644); err != nil {
			return err
		}
		fmt.Printf("Exported source %s to %s.\n", args[0], flagSourcesOutput)
		return nil
	})
}

func runSourcesRemove(_ *cobra.Command, args []string) error {
	return withSourceJobs(func(ctx context.Context, svc *service.JobService) error {
		if err := svc.RemoveLocalSource(ctx, args[0]); err != nil {
			return err
		}
		fmt.Printf("Removed local source %s.\n", args[0])
		return nil
	})
}

func runSourcesTemplate(_ *cobra.Command, args []string) error {
	body, err := sources.EncodeProfileYAML(sources.TemplateProfile(args[0]))
	if err != nil {
		return err
	}
	fmt.Print(string(body))
	return nil
}

func runSourcesVerify(_ *cobra.Command, args []string) error {
	return withSourceJobs(func(ctx context.Context, svc *service.JobService) error {
		if err := svc.SyncSources(ctx, ""); err != nil {
			return err
		}
		cfg, logSvc, err := runtimeConfig()
		if err != nil {
			return err
		}
		cfg.CookieDBPath = flagSourcesDB
		svc.ApplySettings(ctx, cfg)
		result, err := svc.VerifySource(ctx, cfg, logSvc, args[0])
		if result.SourceID != "" {
			fmt.Printf("%s: %s, chapters=%d, images=%d", result.SourceID, result.Status, result.ChaptersFound, result.ImagesFound)
			if len(result.ImageExtensions) > 0 {
				fmt.Printf(", extensions=%s", strings.Join(result.ImageExtensions, ", "))
			}
			fmt.Println()
		}
		return err
	})
}

func readSourceInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func withSourceJobs(fn func(context.Context, *service.JobService) error) error {
	ctx := context.Background()
	svc, closeDB, err := service.OpenJobs(ctx, flagSourcesDB)
	if err != nil {
		return err
	}
	defer closeDB()
	return fn(ctx, svc)
}
