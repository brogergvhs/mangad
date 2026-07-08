package service

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/chapters"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/providers"
)

// ImportCandidate is a download-dir folder not yet tracked as a title.
type ImportCandidate struct {
	Folder       string `json:"folder"`
	ChapterFiles int    `json:"chapter_files"`
}

// ExploreDownloads lists direct subfolders of root that hold .cbz files and
// are not already tracked.
func (s *WantedService) ExploreDownloads(ctx context.Context, root string) ([]ImportCandidate, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve download dir: %w", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read download dir: %w", err)
	}
	titles, err := s.library.ListTitles(ctx)
	if err != nil {
		return nil, err
	}
	tracked := make(map[string]bool, len(titles))
	for _, t := range titles {
		tracked[trackedDir(t)] = true
	}

	var out []ImportCandidate
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), "_tmp") || tracked[e.Name()] {
			continue
		}
		if n := countCBZ(filepath.Join(root, e.Name())); n > 0 {
			out = append(out, ImportCandidate{Folder: e.Name(), ChapterFiles: n})
		}
	}
	return out, nil
}

// ImportFolder tracks an existing folder as a title linked to an AniList entry
// and records its .cbz files as completed downloads.
func (s *WantedService) ImportFolder(ctx context.Context, root, folder string, anilistID int) (library.Title, error) {
	if folder == "" || folder == "." || folder == ".." || strings.ContainsAny(folder, `/\`) {
		return library.Title{}, fmt.Errorf("invalid folder %q", folder)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return library.Title{}, err
	}
	dir := filepath.Join(root, folder)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return library.Title{}, fmt.Errorf("folder %q not found", folder)
	}

	manga, err := s.anilist.Get(ctx, anilistID)
	if err != nil {
		return library.Title{}, err
	}
	manga, err = s.catalog.UpsertManga(ctx, manga)
	if err != nil {
		return library.Title{}, err
	}

	title, err := s.library.AddTitle(ctx, library.AddTitleParams{
		CatalogMangaID:  &manga.ID,
		SourceURL:       localURL(folder),
		DisplayTitle:    displayMangaTitle(manga),
		OutputPath:      folder,
		Monitored:       true,
		RefreshInterval: "24h",
	})
	if err != nil {
		return library.Title{}, err
	}

	// One chapter per label (the first file wins); the chapter list must
	// dedupe by label since (title_id, label) is unique.
	type parsedFile struct {
		file string
		num  int
	}
	byLabel := map[string]parsedFile{}
	var labels []string
	for _, f := range cbzFiles(dir) {
		label, num := parseChapterFile(f)
		if _, seen := byLabel[label]; seen {
			continue
		}
		byLabel[label] = parsedFile{file: f, num: num}
		labels = append(labels, label)
	}

	discovered := make([]chapters.Chapter, 0, len(labels))
	for _, label := range labels {
		p := byLabel[label]
		discovered = append(discovered, chapters.Chapter{Chapter: providers.Chapter{
			Label:   label,
			NumMain: p.num,
			Title:   strings.TrimSuffix(p.file, filepath.Ext(p.file)),
			URL:     localURL(folder + "/" + p.file),
		}})
	}
	if _, err := s.library.UpsertChapters(ctx, title.ID, discovered); err != nil {
		return library.Title{}, err
	}
	for _, label := range labels {
		ch, err := s.library.GetChapterByLabel(ctx, title.ID, label)
		if err != nil {
			continue
		}
		path := filepath.Join(dir, byLabel[label].file)
		var size int64
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
		}
		if err := s.library.MarkDownloadCompleted(ctx, ch.ID, path, size); err != nil {
			return library.Title{}, err
		}
	}
	return s.library.GetTitle(ctx, title.ID)
}

// AddCatalogTitle adds an AniList manga to the library as a source-less title
// (deduped per manga) so its sources can be linked later.
func (s *WantedService) AddCatalogTitle(ctx context.Context, anilistID int) (library.Title, error) {
	manga, err := s.anilist.Get(ctx, anilistID)
	if err != nil {
		return library.Title{}, err
	}
	manga, err = s.catalog.UpsertManga(ctx, manga)
	if err != nil {
		return library.Title{}, err
	}
	if existing, ok, err := s.library.FindByCatalog(ctx, manga.ID); err != nil {
		return library.Title{}, err
	} else if ok {
		return existing, nil
	}
	return s.library.AddTitle(ctx, library.AddTitleParams{
		CatalogMangaID:  &manga.ID,
		SourceURL:       fmt.Sprintf("pending:%d", manga.ID),
		DisplayTitle:    displayMangaTitle(manga),
		Monitored:       true,
		RefreshInterval: "24h",
	})
}

// GetManga returns stored catalog metadata.
func (s *WantedService) GetManga(ctx context.Context, catalogID int64) (catalog.Manga, error) {
	return s.catalog.GetManga(ctx, catalogID)
}

// LinkTitleSource points an imported title at a real source so future refreshes
// and downloads work.
func (s *WantedService) LinkTitleSource(ctx context.Context, titleID, matchID int64) (library.Title, error) {
	match, err := s.catalog.GetMatch(ctx, matchID)
	if err != nil {
		return library.Title{}, err
	}
	if err := s.library.LinkSource(ctx, titleID, match.SourceURL, match.SourceID); err != nil {
		return library.Title{}, err
	}
	return s.library.GetTitle(ctx, titleID)
}

// trackedDir is the folder name a tracked title downloads into.
func trackedDir(t library.Title) string {
	if t.OutputPath != "" {
		return filepath.Base(t.OutputPath)
	}
	return titleOutputDir(t)
}

func localURL(s string) string {
	return "local:" + url.PathEscape(s)
}

func countCBZ(dir string) int {
	return len(cbzFiles(dir))
}

func cbzFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".cbz") {
			out = append(out, e.Name())
		}
	}
	return out
}

var (
	// A leading number keyed to the chapter, e.g. "12: Title", "12 - Title",
	// "33 Kyoto ... part 0", "012.cbz". Ripped collections almost always lead
	// with the chapter number, so a separator (space or punctuation) or the end
	// of the name after it is enough — the leading number wins over any trailing
	// "part N" (which is a sub-part, not a chapter).
	reFileLead    = regexp.MustCompile(`^\s*([0-9]+(?:\.[0-9]+)?)(?:[\s:.)\]_-]|$)`)
	reFileChapter = regexp.MustCompile(`(?i)(?:chapter|episode|ch|ep|c)[\s._-]*([0-9]+(?:\.[0-9]+)?)`)
	reFileNumber  = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`)
)

// parseChapterFile derives a chapter label and main number from a .cbz name.
// The label is normalized (leading zeros stripped) to match scraper output so
// imported chapters merge with a later source refresh.
func parseChapterFile(name string) (label string, num int) {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	raw := ""
	switch {
	case reFileLead.MatchString(stem):
		raw = reFileLead.FindStringSubmatch(stem)[1] // "12: Title" -> 12
	case reFileChapter.MatchString(stem):
		raw = reFileChapter.FindStringSubmatch(stem)[1] // "Vol 2 Ch 7" -> 7
	default:
		if nums := reFileNumber.FindAllString(stem, -1); len(nums) > 0 {
			raw = nums[len(nums)-1] // last standalone number
		}
	}
	if raw == "" {
		return strings.TrimSpace(stem), 0
	}
	mainStr, frac, hasFrac := strings.Cut(raw, ".")
	num, _ = strconv.Atoi(mainStr)
	label = strconv.Itoa(num) // "001" -> "1"
	if hasFrac {
		label += "." + frac // keep fractional part verbatim: "12.05"
	}
	return label, num
}
