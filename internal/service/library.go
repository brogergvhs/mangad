package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/providers"
	"github.com/brogergvhs/mangad/internal/ui"
)

// LibraryService coordinates tracked titles and chapter discovery.
type LibraryService struct {
	repo *library.Repository
}

// RefreshResult describes one refreshed title.
type RefreshResult struct {
	Title library.Title
	Count int
}

// ScanResult describes filesystem verification for completed downloads.
type ScanResult struct {
	Checked int
	Missing int
}

// OpenLibrary opens the library database and applies migrations.
func OpenLibrary(ctx context.Context, dbPath string) (*LibraryService, func(), error) {
	db, err := database.Open(ctx, dbPath)
	if err != nil {
		return nil, nil, err
	}
	if err := database.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, nil, err
	}

	return &LibraryService{repo: library.NewRepository(db)}, func() { _ = db.Close() }, nil
}

// AddTitle tracks a source URL.
func (s *LibraryService) AddTitle(ctx context.Context, params library.AddTitleParams) (library.Title, error) {
	return s.repo.AddTitle(ctx, params)
}

// ListTitles returns tracked titles.
func (s *LibraryService) ListTitles(ctx context.Context) ([]library.Title, error) {
	return s.repo.ListTitles(ctx)
}

// MissingChapters returns a title and its missing chapters.
func (s *LibraryService) MissingChapters(ctx context.Context, id int64) (library.Title, []library.Chapter, error) {
	title, err := s.repo.GetTitle(ctx, id)
	if err != nil {
		return library.Title{}, nil, err
	}
	missing, err := s.repo.ListMissingChapters(ctx, id)
	if err != nil {
		return library.Title{}, nil, err
	}

	return title, missing, nil
}

// RemoveTitle removes a tracked title and returns the removed title.
func (s *LibraryService) RemoveTitle(ctx context.Context, id int64) (library.Title, error) {
	title, err := s.repo.GetTitle(ctx, id)
	if err != nil {
		return library.Title{}, err
	}
	if err := s.repo.RemoveTitle(ctx, id); err != nil {
		return library.Title{}, err
	}

	return title, nil
}

// GetTitle returns one tracked title.
func (s *LibraryService) GetTitle(ctx context.Context, id int64) (library.Title, error) {
	return s.repo.GetTitle(ctx, id)
}

// RefreshTitle refreshes discovered chapters for one title.
func (s *LibraryService) RefreshTitle(
	ctx context.Context,
	cfg *config.Config,
	logSvc *ui.Logger,
	title library.Title,
) (RefreshResult, error) {
	downloadSvc, err := NewDefaultDownloadService(cfg, logSvc, nil)
	if err != nil {
		return RefreshResult{}, err
	}

	chapters, err := downloadSvc.FetchChapters(ctx, title.SourceURL)
	if err != nil {
		return RefreshResult{}, err
	}
	count, err := s.repo.UpsertChapters(ctx, title.ID, chapters)
	if err != nil {
		return RefreshResult{}, err
	}

	return RefreshResult{Title: title, Count: count}, nil
}

// RefreshMonitored refreshes all monitored titles.
func (s *LibraryService) RefreshMonitored(
	ctx context.Context,
	cfg *config.Config,
	logSvc *ui.Logger,
) ([]RefreshResult, error) {
	titles, err := s.repo.ListTitles(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]RefreshResult, 0, len(titles))
	for _, title := range titles {
		if !title.Monitored {
			continue
		}
		result, err := s.RefreshTitle(ctx, cfg, logSvc, title)
		if err != nil {
			return nil, fmt.Errorf("refresh %s: %w", title.DisplayTitle, err)
		}
		results = append(results, result)
	}

	return results, nil
}

// ScanDownloads verifies completed download files still exist.
func (s *LibraryService) ScanDownloads(ctx context.Context, titleID int64) (ScanResult, error) {
	downloads, err := s.repo.ListCompletedDownloads(ctx, titleID)
	if err != nil {
		return ScanResult{}, err
	}

	var result ScanResult
	for _, download := range downloads {
		result.Checked++
		if _, err := os.Stat(download.OutputFile); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return result, fmt.Errorf("check %s: %w", download.OutputFile, err)
		}

		result.Missing++
		if err := s.repo.MarkDownloadFailed(ctx, download.ChapterID, fmt.Errorf("output file missing: %s", download.OutputFile)); err != nil {
			return result, err
		}
	}

	return result, nil
}

// DownloadMissing downloads every missing chapter for a title.
func (s *LibraryService) DownloadMissing(
	ctx context.Context,
	cfg *config.Config,
	logSvc *ui.Logger,
	titleID int64,
) ([]ChapterDownloadResult, error) {
	title, missing, err := s.MissingChapters(ctx, titleID)
	if err != nil {
		return nil, err
	}

	cfg, err = configForTitle(cfg, title)
	if err != nil {
		return nil, err
	}
	results := make([]ChapterDownloadResult, 0, len(missing))
	for _, chapter := range missing {
		result, err := s.downloadChapter(ctx, cfg, logSvc, chapter)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}

	return results, nil
}

// DownloadMonitoredMissing downloads missing chapters for every monitored title.
func (s *LibraryService) DownloadMonitoredMissing(
	ctx context.Context,
	cfg *config.Config,
	logSvc *ui.Logger,
) ([]ChapterDownloadResult, error) {
	titles, err := s.repo.ListTitles(ctx)
	if err != nil {
		return nil, err
	}

	var results []ChapterDownloadResult
	for _, title := range titles {
		if !title.Monitored {
			continue
		}
		titleResults, err := s.DownloadMissing(ctx, cfg, logSvc, title.ID)
		if err != nil {
			return results, fmt.Errorf("download missing for %s: %w", title.DisplayTitle, err)
		}
		results = append(results, titleResults...)
	}

	return results, nil
}

// DownloadChapterLabel downloads one discovered chapter by label.
func (s *LibraryService) DownloadChapterLabel(
	ctx context.Context,
	cfg *config.Config,
	logSvc *ui.Logger,
	titleID int64,
	label string,
) (ChapterDownloadResult, error) {
	title, err := s.repo.GetTitle(ctx, titleID)
	if err != nil {
		return ChapterDownloadResult{}, err
	}
	chapter, err := s.repo.GetChapterByLabel(ctx, titleID, label)
	if err != nil {
		return ChapterDownloadResult{}, err
	}

	titleCfg, err := configForTitle(cfg, title)
	if err != nil {
		return ChapterDownloadResult{}, err
	}
	return s.downloadChapter(ctx, titleCfg, logSvc, chapter)
}

func (s *LibraryService) downloadChapter(
	ctx context.Context,
	cfg *config.Config,
	logSvc *ui.Logger,
	chapter library.Chapter,
) (ChapterDownloadResult, error) {
	if err := s.repo.MarkDownloadStarted(ctx, chapter.ID); err != nil {
		return ChapterDownloadResult{}, err
	}

	downloadSvc, err := NewDefaultDownloadService(cfg, logSvc, nil)
	if err != nil {
		return ChapterDownloadResult{}, err
	}

	result, err := downloadSvc.DownloadChapter(ctx, serviceChapter(chapter))
	if err != nil {
		_ = s.repo.MarkDownloadFailed(ctx, chapter.ID, err)
		return ChapterDownloadResult{}, err
	}
	if err := s.repo.MarkDownloadCompleted(ctx, chapter.ID, result.OutputFile, result.Bytes); err != nil {
		return ChapterDownloadResult{}, err
	}

	return result, nil
}

func serviceChapter(chapter library.Chapter) chapters.Chapter {
	return chapters.Chapter{Chapter: providers.Chapter{
		URL:        chapter.URL,
		Title:      chapter.Title,
		NumMain:    chapter.NumberMain,
		SuffixType: chapter.SuffixType,
		SuffixNum:  chapter.SuffixNum,
		Label:      chapter.Label,
	}}
}

func configForTitle(cfg *config.Config, title library.Title) (*config.Config, error) {
	next := *cfg
	root, err := filepath.Abs(next.DownloadDir)
	if err != nil {
		return nil, fmt.Errorf("resolve download root: %w", err)
	}
	if title.OutputPath != "" {
		output := title.OutputPath
		if !filepath.IsAbs(output) {
			output = filepath.Join(root, output)
		}
		output, err = filepath.Abs(output)
		if err != nil {
			return nil, fmt.Errorf("resolve output path: %w", err)
		}
		if output != root && !strings.HasPrefix(output, root+string(os.PathSeparator)) {
			return nil, fmt.Errorf("output path %q is outside download root %q", title.OutputPath, next.DownloadDir)
		}
		next.Output = output
	} else {
		next.Output = filepath.Join(root, titleOutputDir(title))
	}
	return &next, nil
}

func titleOutputDir(title library.Title) string {
	name := strings.TrimSpace(title.DisplayTitle)
	if name == "" {
		return fmt.Sprintf("title_%d", title.ID)
	}

	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}

	out := strings.Trim(b.String(), "_")
	if out == "" {
		return fmt.Sprintf("title_%d", title.ID)
	}

	return out
}
