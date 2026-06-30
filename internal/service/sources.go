package service

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"

	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/ui"
)

type sourceService struct {
	repo *sources.Repository
}

// SourceVerifyResult is the latest verification sample for a source.
type SourceVerifyResult struct {
	SourceID        string   `json:"source_id"`
	Status          string   `json:"status"`
	ChaptersFound   int      `json:"chapters_found"`
	ImagesFound     int      `json:"images_found"`
	ImageExtensions []string `json:"image_extensions"`
	Error           string   `json:"error,omitempty"`
}

func newSourceService(db *sql.DB) *sourceService {
	return &sourceService{repo: sources.NewRepository(db)}
}

func (s *sourceService) SyncBuiltIn(ctx context.Context) error {
	profiles, err := sources.BuiltInProfiles()
	if err != nil {
		return err
	}
	return s.repo.Sync(ctx, profiles, sources.OriginBuiltin)
}

func (s *sourceService) SyncRegistry(ctx context.Context, registryURL string) error {
	profiles, err := sources.FetchRegistry(ctx, registryURL)
	if err != nil {
		return err
	}
	return s.repo.Sync(ctx, profiles, sources.OriginRegistry)
}

func (s *sourceService) ListSources(ctx context.Context) ([]sources.Source, error) {
	return s.repo.List(ctx)
}

func (s *sourceService) ImportLocal(ctx context.Context, profile sources.Profile) error {
	return s.repo.Sync(ctx, []sources.Profile{profile}, sources.OriginLocal)
}

func (s *sourceService) RemoveLocal(ctx context.Context, id string) error {
	return s.repo.RemoveLocal(ctx, strings.TrimSpace(id))
}

func (s *sourceService) ExportSource(ctx context.Context, id string) ([]byte, error) {
	src, err := s.repo.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	return sources.EncodeProfileYAML(src.Profile)
}

func (s *sourceService) VerifySource(ctx context.Context, cfg *config.Config, logSvc *ui.Logger, id string) (SourceVerifyResult, error) {
	src, err := s.repo.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return SourceVerifyResult{}, err
	}
	result := SourceVerifyResult{SourceID: src.ID, Status: sources.StatusBroken}
	checkErr := s.verify(ctx, cfg, logSvc, src, &result)
	if checkErr != nil {
		result.Error = checkErr.Error()
	}
	if err := s.repo.UpdateCheck(ctx, src.ID, result.Status, result.Error, result.ChaptersFound, result.ImagesFound, result.ImageExtensions); err != nil {
		return result, err
	}
	if checkErr != nil {
		return result, checkErr
	}
	return result, nil
}

func (s *sourceService) verify(ctx context.Context, cfg *config.Config, logSvc *ui.Logger, src sources.Source, result *SourceVerifyResult) error {
	if !src.Enabled {
		return fmt.Errorf("source %s is disabled", src.ID)
	}
	cfgCopy := ConfigForSource(*cfg, src, SourceConfigOptions{})

	downloadSvc, err := NewSourceDownloadService(&cfgCopy, logSvc, nil, src.Scraper)
	if err != nil {
		return err
	}
	chapters, err := downloadSvc.FetchChapters(ctx, src.SampleMangaURL)
	if err != nil {
		return err
	}
	result.ChaptersFound = len(chapters)
	if len(chapters) < src.MinChapters {
		return fmt.Errorf("found %d chapters, expected at least %d", len(chapters), src.MinChapters)
	}
	if len(chapters) == 0 {
		return fmt.Errorf("no chapters found")
	}
	images, err := downloadSvc.FetchImages(ctx, chapters[0])
	if err != nil {
		return err
	}
	result.ImagesFound = len(images)
	result.ImageExtensions = imageExtensions(images)
	if len(images) == 0 {
		return fmt.Errorf("no images found in sample chapter")
	}
	result.Status = sources.StatusHealthy
	return nil
}

func imageExtensions(images []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, image := range images {
		image, _, _ = strings.Cut(image, "?")
		ext := strings.TrimPrefix(strings.ToLower(path.Ext(image)), ".")
		if ext == "" || seen[ext] {
			continue
		}
		seen[ext] = true
		out = append(out, ext)
	}
	return out
}
