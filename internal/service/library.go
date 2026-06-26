package service

import (
	"context"
	"fmt"

	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/database"
	"github.com/brogergvhs/mangad/internal/library"
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
