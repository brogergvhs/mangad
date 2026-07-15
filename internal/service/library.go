package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/providers"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/ui"
)

// LibraryService coordinates tracked titles and chapter discovery.
type LibraryService struct {
	repo     *library.Repository
	sources  *sources.Repository
	syncOnce sync.Once
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

	svc := newLibraryService(db)
	if _, err := svc.ReconcileStartedDownloads(ctx); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	if _, err := svc.ScanDownloads(ctx, 0); err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return svc, func() { _ = db.Close() }, nil
}

func newLibraryService(db *sql.DB) *LibraryService {
	return &LibraryService{repo: library.NewRepository(db), sources: sources.NewRepository(db)}
}

// ReconcileStartedDownloads marks interrupted downloads as failed.
func (s *LibraryService) ReconcileStartedDownloads(ctx context.Context) (int64, error) {
	return s.repo.ReconcileStartedDownloads(ctx)
}

// SetRefreshInterval sets a title's custom refresh cadence (empty = global).
func (s *LibraryService) SetRefreshInterval(ctx context.Context, id int64, interval string) error {
	return s.repo.SetRefreshInterval(ctx, id, interval)
}

// SetMonitored toggles monitoring for a tracked title.
func (s *LibraryService) SyncVolumeFiles(ctx context.Context, titleID int64, dir string, pageCount func(string) int) (int, error) {
	return s.repo.SyncVolumeFiles(ctx, titleID, dir, pageCount)
}

func (s *LibraryService) Volumes(ctx context.Context, titleID int64) ([]library.Volume, error) {
	return s.repo.Volumes(ctx, titleID)
}

func (s *LibraryService) HasVolumes(ctx context.Context, titleID int64) (bool, error) {
	return s.repo.HasVolumes(ctx, titleID)
}

func (s *LibraryService) GetVolume(ctx context.Context, id int64) (library.Volume, error) {
	return s.repo.GetVolume(ctx, id)
}

func (s *LibraryService) SetVolumeRead(ctx context.Context, id int64, read bool) error {
	return s.repo.SetVolumeRead(ctx, id, read)
}

func (s *LibraryService) VolumeCover(ctx context.Context, id int64) ([]byte, string, error) {
	return s.repo.VolumeCover(ctx, id)
}

func (s *LibraryService) SetVolumeCover(ctx context.Context, id int64, blob []byte, mime string) error {
	return s.repo.SetVolumeCover(ctx, id, blob, mime)
}

func (s *LibraryService) ToggleFavourite(ctx context.Context, titleID int64) (bool, error) {
	return s.repo.ToggleFavourite(ctx, titleID)
}

func (s *LibraryService) SetFavourite(ctx context.Context, userID, titleID int64) error {
	return s.repo.SetFavourite(ctx, userID, titleID)
}

func (s *LibraryService) SetMonitored(ctx context.Context, id int64, monitored bool) error {
	return s.repo.SetMonitored(ctx, id, monitored)
}

// AddTitle tracks a source URL.
func (s *LibraryService) AddTitle(ctx context.Context, params library.AddTitleParams) (library.Title, error) {
	if params.SourceID == "" {
		if src, ok := s.sourceForURL(ctx, params.SourceURL); ok {
			params.SourceID = src.ID
		}
	}
	return s.repo.AddTitle(ctx, params)
}

// ListTitles returns tracked titles.
func (s *LibraryService) ListTitles(ctx context.Context) ([]library.Title, error) {
	return s.repo.ListTitles(ctx)
}

// ResetFailedDownloads clears the attempt cap on a title's failed downloads.
func (s *LibraryService) ResetFailedDownloads(ctx context.Context, titleID int64) error {
	return s.repo.ResetFailedDownloads(ctx, titleID)
}

// TitlesByProvider maps a catalog provider's manga IDs to tracked title IDs.
func (s *LibraryService) TitlesByProvider(ctx context.Context, provider string) (map[string]int64, error) {
	return s.repo.TitlesByProvider(ctx, provider)
}

// ListTitleSources returns all sources linked to a title.
func (s *LibraryService) ListTitleSources(ctx context.Context, titleID int64) ([]library.LinkedSource, error) {
	return s.repo.ListTitleSources(ctx, titleID)
}

// UnlinkSource removes a linked source from a title.
func (s *LibraryService) UnlinkSource(ctx context.Context, titleID int64, url string) error {
	return s.repo.UnlinkSource(ctx, titleID, url)
}

// ListChapters returns all discovered chapters for a title with download state.
func (s *LibraryService) ListChapters(ctx context.Context, titleID int64) ([]library.ChapterStatus, error) {
	return s.repo.ListChapters(ctx, titleID)
}

// ReaderProgress returns downloaded chapters and read state for a title.
func (s *LibraryService) ReaderProgress(ctx context.Context, titleID int64) (library.TitleReadProgress, error) {
	return s.repo.ReaderProgress(ctx, titleID)
}

// ChapterReadStatus returns reader/download state for one chapter.
func (s *LibraryService) ChapterReadStatus(ctx context.Context, chapterID int64) (library.ChapterReadStatus, error) {
	return s.repo.GetChapterReadStatus(ctx, chapterID)
}

// MarkPageRead records one completed page for reader resume/progress.
func (s *LibraryService) MarkPageRead(ctx context.Context, chapterID int64, page, totalPages int) (library.ChapterReadStatus, error) {
	return s.repo.MarkPageRead(ctx, chapterID, page, totalPages)
}

// MarkChapterRead records a completed chapter.
func (s *LibraryService) MarkChapterRead(ctx context.Context, chapterID int64) (library.ChapterReadStatus, error) {
	return s.repo.MarkChapterRead(ctx, chapterID)
}

// MarkChapterUnread clears read progress for a chapter.
func (s *LibraryService) MarkChapterUnread(ctx context.Context, chapterID int64) (library.ChapterReadStatus, error) {
	return s.repo.MarkChapterUnread(ctx, chapterID)
}

// MarkChapterRangeRead marks downloaded chapters in a title range read.
// MarkChaptersReadThrough marks chapters up to a number read (AniList pull).
func (s *LibraryService) MarkChaptersReadThrough(ctx context.Context, titleID int64, maxNumber int) (int, error) {
	return s.repo.MarkChaptersReadThrough(ctx, titleID, maxNumber)
}

func (s *LibraryService) MarkChapterRangeRead(ctx context.Context, titleID int64, from, to string) (int, error) {
	return s.repo.MarkChapterRangeRead(ctx, titleID, from, to)
}

// MarkChapterRangeUnread clears read progress in a title range.
func (s *LibraryService) MarkChapterRangeUnread(ctx context.Context, titleID int64, from, to string) (int, error) {
	return s.repo.MarkChapterRangeUnread(ctx, titleID, from, to)
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
	logSvc ui.Log,
	title library.Title,
) (RefreshResult, error) {
	if !strings.HasPrefix(title.SourceURL, "http") {
		return RefreshResult{}, fmt.Errorf("title %q has no linked source to refresh from", title.DisplayTitle)
	}
	downloadSvc, err := s.downloadServiceForTitle(ctx, cfg, logSvc, nil, title)
	if err != nil {
		return RefreshResult{}, err
	}

	chapters, finalURL, err := downloadSvc.FetchChapters(ctx, title.SourceURL)
	if err != nil {
		return RefreshResult{}, err
	}
	if finalURL != "" && finalURL != title.SourceURL && strings.HasPrefix(finalURL, "http") {
		if err := s.repo.UpdateSourceURL(ctx, title.ID, title.SourceURL, finalURL); err != nil {
			logSvc.Infof("Could not persist moved source URL for %q: %v\n", title.DisplayTitle, err)
		} else {
			logSvc.Infof("Source URL for %q moved: %s -> %s\n", title.DisplayTitle, title.SourceURL, finalURL)
			title.SourceURL = finalURL
		}
	}
	if len(chapters) == 0 {
		// Zero chapters means a challenge page or selector drift, not a real
		// result; erroring keeps last_refreshed_at unstamped and surfaces the
		// failure in the jobs table instead of silently succeeding.
		return RefreshResult{}, fmt.Errorf("no chapters found at %s — the site may need FlareSolverr, or the source URL is wrong", title.SourceURL)
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
	logSvc ui.Log,
) ([]RefreshResult, error) {
	titles, err := s.repo.ListTitles(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]RefreshResult, 0, len(titles))
	var errs []error
	for _, title := range titles {
		if !title.Monitored {
			continue
		}
		result, err := s.RefreshTitle(ctx, cfg, logSvc, title)
		if err != nil {
			errs = append(errs, fmt.Errorf("refresh %s: %w", title.DisplayTitle, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		results = append(results, result)
	}

	return results, errors.Join(errs...)
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
		info, err := os.Stat(download.OutputFile)
		if err == nil {
			if err := s.repo.MarkDownloadCompleted(ctx, download.ChapterID, download.OutputFile, info.Size(), cbzPageCount(download.OutputFile)); err != nil {
				return result, err
			}
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
	logSvc ui.Log,
	titleID int64,
	progress ProgressManager,
) ([]ChapterDownloadResult, error) {
	title, missing, err := s.MissingChapters(ctx, titleID)
	if err != nil {
		return nil, err
	}

	cfg, err = configForTitle(cfg, title)
	if err != nil {
		return nil, err
	}
	if has, err := s.repo.HasVolumes(ctx, titleID); err == nil && has {
		chaptersDir, _, err := s.EnsureVolumeChapterSplit(ctx, cfg, title)
		if err != nil {
			return nil, err
		}
		cfg.Output = chaptersDir
	}
	downloadSvc, err := s.downloadServiceForTitle(ctx, cfg, logSvc, progress, title)
	if err != nil {
		return nil, err
	}
	// A run of consecutive failures means the source is broken, not that the
	// job is progressing; stop instead of hammering every remaining chapter.
	const maxConsecutiveFailures = 10
	consecutive := 0
	results := make([]ChapterDownloadResult, 0, len(missing))
	var errs []error
	for _, chapter := range missing {
		result, err := s.downloadChapter(ctx, downloadSvc, chapter)
		if err != nil {
			errs = append(errs, fmt.Errorf("chapter %s: %w", chapter.Label, err))
			if ctx.Err() != nil {
				break
			}
			if consecutive++; consecutive >= maxConsecutiveFailures {
				errs = append(errs, fmt.Errorf("stopping after %d consecutive chapter failures", consecutive))
				break
			}
			continue
		}
		consecutive = 0
		results = append(results, result)
	}

	return results, errors.Join(errs...)
}

// DownloadMonitoredMissing downloads missing chapters for every monitored title.
func (s *LibraryService) DownloadMonitoredMissing(
	ctx context.Context,
	cfg *config.Config,
	logSvc ui.Log,
) ([]ChapterDownloadResult, error) {
	titles, err := s.repo.ListTitles(ctx)
	if err != nil {
		return nil, err
	}

	var results []ChapterDownloadResult
	var errs []error
	for _, title := range titles {
		if !title.Monitored {
			continue
		}
		titleResults, err := s.DownloadMissing(ctx, cfg, logSvc, title.ID, nil)
		results = append(results, titleResults...)
		if err != nil {
			errs = append(errs, fmt.Errorf("download missing for %s: %w", title.DisplayTitle, err))
			if ctx.Err() != nil {
				break
			}
		}
	}

	return results, errors.Join(errs...)
}

// DownloadChapterLabel downloads one discovered chapter by label.
func (s *LibraryService) DownloadChapterLabel(
	ctx context.Context,
	cfg *config.Config,
	logSvc ui.Log,
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
	downloadSvc, err := s.downloadServiceForTitle(ctx, titleCfg, logSvc, nil, title)
	if err != nil {
		return ChapterDownloadResult{}, err
	}
	return s.downloadChapter(ctx, downloadSvc, chapter)
}

func (s *LibraryService) downloadChapter(
	ctx context.Context,
	downloadSvc *DownloadService,
	chapter library.Chapter,
) (ChapterDownloadResult, error) {
	if err := s.repo.MarkDownloadStarted(ctx, chapter.ID); err != nil {
		return ChapterDownloadResult{}, err
	}

	// Outcomes must be persisted even when ctx expired mid-download, or the
	// row strands as 'started' and startup reconciliation burns an attempt.
	markCtx := context.WithoutCancel(ctx)
	result, err := downloadSvc.DownloadChapter(ctx, serviceChapter(chapter))
	if err != nil {
		if markErr := s.repo.MarkDownloadFailed(markCtx, chapter.ID, err); markErr != nil {
			return ChapterDownloadResult{}, fmt.Errorf("%w; mark download failed: %v", err, markErr)
		}
		return ChapterDownloadResult{}, err
	}
	if err := s.repo.MarkDownloadCompleted(markCtx, chapter.ID, result.OutputFile, result.Bytes, result.Images); err != nil {
		return ChapterDownloadResult{}, err
	}

	return result, nil
}

func (s *LibraryService) downloadServiceForTitle(
	ctx context.Context,
	cfg *config.Config,
	logSvc ui.Log,
	progress ProgressManager,
	title library.Title,
) (*DownloadService, error) {
	if src, ok := s.sourceForTitle(ctx, title); ok {
		next := ConfigForSource(*cfg, src, SourceConfigOptions{})
		return NewSourceDownloadService(&next, logSvc, progress, src.Scraper)
	}
	return NewDefaultDownloadService(cfg, logSvc, progress)
}

func (s *LibraryService) sourceForTitle(ctx context.Context, title library.Title) (sources.Source, bool) {
	if strings.TrimSpace(title.SourceID) != "" {
		if list, err := s.listSources(ctx); err == nil {
			for _, src := range list {
				if src.ID == title.SourceID && src.Enabled {
					return src, true
				}
			}
		}
	}
	return s.sourceForURL(ctx, title.SourceURL)
}

func (s *LibraryService) sourceForURL(ctx context.Context, target string) (sources.Source, bool) {
	list, err := s.listSources(ctx)
	if err != nil {
		return sources.Source{}, false
	}
	return MatchSourceForURL(list, target)
}

func (s *LibraryService) listSources(ctx context.Context) ([]sources.Source, error) {
	profiles, err := sources.BuiltInProfiles()
	if err != nil {
		return nil, err
	}
	if s.sources == nil {
		out := make([]sources.Source, 0, len(profiles))
		for _, profile := range profiles {
			out = append(out, sourceFromProfile(profile, sources.OriginBuiltin))
		}
		return out, nil
	}
	// Built-in profiles only change with the binary; one sync per process.
	var syncErr error
	s.syncOnce.Do(func() {
		syncErr = s.sources.Sync(ctx, profiles, sources.OriginBuiltin)
	})
	if syncErr != nil {
		return nil, syncErr
	}
	return s.sources.List(ctx)
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

// TitleFilesDir resolves the folder holding a title's downloads, strictly
// inside the download root (never the root itself).
func (s *LibraryService) TitleFilesDir(cfg *config.Config, title library.Title) (string, error) {
	titleCfg, err := configForTitle(cfg, title)
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(titleCfg.DownloadDir)
	if err != nil {
		return "", err
	}
	dir := titleCfg.Output
	if dir == root || !strings.HasPrefix(dir, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing to touch %q: not strictly inside the download root", dir)
	}
	return dir, nil
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
