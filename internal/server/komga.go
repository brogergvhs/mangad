package server

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brogergvhs/kaodoku/internal/auth"
	"github.com/brogergvhs/kaodoku/internal/library"
	"github.com/brogergvhs/kaodoku/internal/service"
	"github.com/brogergvhs/kaodoku/internal/util"
)

// Komga-compatible read API under /komga, so apps built for Komga (Mihon,
// Komelia, Panels) can browse, read, and sync progress. Response shapes
// follow Komga master and the field lists Mihon's decoders require.
const komgaLibraryID = "1"

type komga struct {
	svc *service.JobService
}

func registerKomga(mux *http.ServeMux, svc *service.JobService) {
	k := &komga{svc: svc}
	mux.HandleFunc("GET /komga/api/v2/users/me", k.me)
	mux.HandleFunc("GET /komga/api/v1/libraries", k.libraries)
	mux.HandleFunc("GET /komga/api/v1/libraries/{id}", k.library)
	mux.HandleFunc("GET /komga/api/v1/series", k.seriesList)
	mux.HandleFunc("GET /komga/api/v1/series/{id}", k.seriesGet)
	mux.HandleFunc("GET /komga/api/v1/series/{id}/books", k.seriesBooks)
	mux.HandleFunc("GET /komga/api/v1/series/{id}/thumbnail", k.seriesThumbnail)
	mux.HandleFunc("GET /komga/api/v1/books/{id}", k.bookGet)
	mux.HandleFunc("GET /komga/api/v1/books/{id}/pages", k.bookPages)
	mux.HandleFunc("GET /komga/api/v1/books/{id}/pages/{page}", k.bookPage)
	mux.HandleFunc("GET /komga/api/v1/books/{id}/thumbnail", k.bookThumbnail)
	mux.HandleFunc("GET /komga/api/v1/books/{id}/file", k.bookFile)
	mux.HandleFunc("PATCH /komga/api/v1/books/{id}/read-progress", k.bookReadProgress)
	mux.HandleFunc("DELETE /komga/api/v1/books/{id}/read-progress", k.bookReadProgressDelete)
	mux.HandleFunc("GET /komga/api/v2/series/{id}/read-progress/tachiyomi", k.tachiyomiProgress)
	mux.HandleFunc("PUT /komga/api/v2/series/{id}/read-progress/tachiyomi", k.tachiyomiProgressPut)
	mux.HandleFunc("GET /komga/api/v1/collections", k.emptyPage)
	mux.HandleFunc("GET /komga/api/v1/readlists", k.emptyPage)
	mux.HandleFunc("GET /komga/api/v1/genres", k.emptyStrings)
	mux.HandleFunc("GET /komga/api/v1/tags", k.emptyStrings)
	mux.HandleFunc("GET /komga/api/v1/publishers", k.emptyStrings)
	mux.HandleFunc("GET /komga/api/v1/authors", k.emptyList)
	mux.HandleFunc("GET /komga/series/{id}", k.seriesWeb)
	mux.HandleFunc("GET /komga/book/{id}", k.bookWeb)
}

// --- DTOs: field sets mirror Komga; REQUIRED fields per Mihon must never be
// omitted or null (its decoder has ignoreUnknownKeys but no null coercion).

type komgaSort struct {
	Empty    bool `json:"empty"`
	Sorted   bool `json:"sorted"`
	Unsorted bool `json:"unsorted"`
}

type komgaPageable struct {
	Sort       komgaSort `json:"sort"`
	Offset     int       `json:"offset"`
	PageNumber int       `json:"pageNumber"`
	PageSize   int       `json:"pageSize"`
	Paged      bool      `json:"paged"`
	Unpaged    bool      `json:"unpaged"`
}

type komgaPage struct {
	Content          any           `json:"content"`
	Pageable         komgaPageable `json:"pageable"`
	Last             bool          `json:"last"`
	TotalPages       int           `json:"totalPages"`
	TotalElements    int           `json:"totalElements"`
	Size             int           `json:"size"`
	Number           int           `json:"number"`
	Sort             komgaSort     `json:"sort"`
	First            bool          `json:"first"`
	NumberOfElements int           `json:"numberOfElements"`
	Empty            bool          `json:"empty"`
}

type komgaAuthor struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type komgaSeriesMetadata struct {
	Status               string   `json:"status"`
	StatusLock           bool     `json:"statusLock"`
	Title                string   `json:"title"`
	TitleLock            bool     `json:"titleLock"`
	TitleSort            string   `json:"titleSort"`
	TitleSortLock        bool     `json:"titleSortLock"`
	Summary              string   `json:"summary"`
	SummaryLock          bool     `json:"summaryLock"`
	ReadingDirection     string   `json:"readingDirection"`
	ReadingDirectionLock bool     `json:"readingDirectionLock"`
	Publisher            string   `json:"publisher"`
	PublisherLock        bool     `json:"publisherLock"`
	AgeRating            *int     `json:"ageRating"`
	AgeRatingLock        bool     `json:"ageRatingLock"`
	Language             string   `json:"language"`
	LanguageLock         bool     `json:"languageLock"`
	Genres               []string `json:"genres"`
	GenresLock           bool     `json:"genresLock"`
	Tags                 []string `json:"tags"`
	TagsLock             bool     `json:"tagsLock"`
	TotalBookCount       *int     `json:"totalBookCount"`
	TotalBookCountLock   bool     `json:"totalBookCountLock"`
	SharingLabels        []string `json:"sharingLabels"`
	SharingLabelsLock    bool     `json:"sharingLabelsLock"`
	Created              string   `json:"created"`
	LastModified         string   `json:"lastModified"`
}

type komgaBooksMetadata struct {
	Authors       []komgaAuthor `json:"authors"`
	Tags          []string      `json:"tags"`
	ReleaseDate   *string       `json:"releaseDate"`
	Summary       string        `json:"summary"`
	SummaryNumber string        `json:"summaryNumber"`
	Created       string        `json:"created"`
	LastModified  string        `json:"lastModified"`
}

type komgaSeries struct {
	ID                   string              `json:"id"`
	LibraryID            string              `json:"libraryId"`
	Name                 string              `json:"name"`
	URL                  string              `json:"url"`
	Created              string              `json:"created"`
	LastModified         string              `json:"lastModified"`
	FileLastModified     string              `json:"fileLastModified"`
	BooksCount           int                 `json:"booksCount"`
	BooksReadCount       int                 `json:"booksReadCount"`
	BooksUnreadCount     int                 `json:"booksUnreadCount"`
	BooksInProgressCount int                 `json:"booksInProgressCount"`
	Metadata             komgaSeriesMetadata `json:"metadata"`
	BooksMetadata        komgaBooksMetadata  `json:"booksMetadata"`
	Deleted              bool                `json:"deleted"`
	Oneshot              bool                `json:"oneshot"`
}

type komgaMedia struct {
	Status               string `json:"status"`
	MediaType            string `json:"mediaType"`
	PagesCount           int    `json:"pagesCount"`
	Comment              string `json:"comment"`
	EpubDivinaCompatible bool   `json:"epubDivinaCompatible"`
	MediaProfile         string `json:"mediaProfile"`
}

type komgaBookMetadata struct {
	Title           string        `json:"title"`
	TitleLock       bool          `json:"titleLock"`
	Summary         string        `json:"summary"`
	SummaryLock     bool          `json:"summaryLock"`
	Number          string        `json:"number"`
	NumberLock      bool          `json:"numberLock"`
	NumberSort      float64       `json:"numberSort"`
	NumberSortLock  bool          `json:"numberSortLock"`
	ReleaseDate     *string       `json:"releaseDate"`
	ReleaseDateLock bool          `json:"releaseDateLock"`
	Authors         []komgaAuthor `json:"authors"`
	AuthorsLock     bool          `json:"authorsLock"`
	Tags            []string      `json:"tags"`
	TagsLock        bool          `json:"tagsLock"`
	Created         string        `json:"created"`
	LastModified    string        `json:"lastModified"`
}

type komgaReadProgress struct {
	Page         int    `json:"page"`
	Completed    bool   `json:"completed"`
	ReadDate     string `json:"readDate"`
	Created      string `json:"created"`
	LastModified string `json:"lastModified"`
	DeviceID     string `json:"deviceId"`
	DeviceName   string `json:"deviceName"`
}

type komgaBook struct {
	ID               string             `json:"id"`
	SeriesID         string             `json:"seriesId"`
	SeriesTitle      string             `json:"seriesTitle"`
	LibraryID        string             `json:"libraryId"`
	Name             string             `json:"name"`
	URL              string             `json:"url"`
	Number           int                `json:"number"`
	Created          string             `json:"created"`
	LastModified     string             `json:"lastModified"`
	FileLastModified string             `json:"fileLastModified"`
	SizeBytes        int64              `json:"sizeBytes"`
	Size             string             `json:"size"`
	Media            komgaMedia         `json:"media"`
	Metadata         komgaBookMetadata  `json:"metadata"`
	ReadProgress     *komgaReadProgress `json:"readProgress"`
	Deleted          bool               `json:"deleted"`
	Oneshot          bool               `json:"oneshot"`
}

type komgaPageDto struct {
	Number    int    `json:"number"`
	FileName  string `json:"fileName"`
	MediaType string `json:"mediaType"`
}

func komgaTime(t time.Time) string {
	if t.IsZero() {
		t = time.Unix(0, 0)
	}
	return t.UTC().Format("2006-01-02T15:04:05") + "Z"
}

func komgaWrite(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func komgaPageOf(content any, n, page, size int, unpaged bool) komgaPage {
	if unpaged || size <= 0 {
		return komgaPage{
			Content:  content,
			Pageable: komgaPageable{Sort: komgaSort{Empty: true, Unsorted: true}, Unpaged: true},
			Last:     true, TotalPages: 1, TotalElements: n, Size: n, Number: 0,
			Sort: komgaSort{Empty: true, Unsorted: true}, First: true,
			NumberOfElements: n, Empty: n == 0,
		}
	}
	totalPages := (n + size - 1) / size
	if totalPages == 0 {
		totalPages = 1
	}
	count := n - page*size
	if count > size {
		count = size
	}
	if count < 0 {
		count = 0
	}
	return komgaPage{
		Content: content,
		Pageable: komgaPageable{
			Sort: komgaSort{Empty: true, Unsorted: true}, Offset: page * size,
			PageNumber: page, PageSize: size, Paged: true,
		},
		Last: page >= totalPages-1, TotalPages: totalPages, TotalElements: n,
		Size: size, Number: page, Sort: komgaSort{Empty: true, Unsorted: true},
		First: page == 0, NumberOfElements: count, Empty: count == 0,
	}
}

// --- handlers

func (k *komga) seriesWeb(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/library/"+r.PathValue("id"), http.StatusSeeOther)
}

func (k *komga) bookWeb(w http.ResponseWriter, r *http.Request) {
	ref, err := k.resolveBook(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/library/"+strconv.FormatInt(ref.titleID, 10), http.StatusSeeOther)
}

func (k *komga) me(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	roles := []string{"USER", "FILE_DOWNLOAD", "PAGE_STREAMING"}
	if user.Can(auth.PermUsersManage) {
		roles = append([]string{"ADMIN"}, roles...)
	}
	komgaWrite(w, map[string]any{
		"id":                 strconv.FormatInt(user.ID, 10),
		"email":              user.Username,
		"roles":              roles,
		"sharedAllLibraries": true,
		"sharedLibrariesIds": []string{},
		"labelsAllow":        []string{},
		"labelsExclude":      []string{},
	})
}

func komgaLibrary() map[string]any {
	return map[string]any{
		"id": komgaLibraryID, "name": "Kaodoku", "root": "",
		"importComicInfoBook": false, "importComicInfoSeries": false,
		"importComicInfoCollection": false, "importComicInfoReadList": false,
		"importComicInfoSeriesAppendVolume": false, "importEpubBook": false,
		"importEpubSeries": false, "importMylarSeries": false,
		"importLocalArtwork": false, "importBarcodeIsbn": false,
		"scanForceModifiedTime": false, "scanInterval": "DISABLED",
		"scanOnStartup": false, "scanCbx": true, "scanPdf": false, "scanEpub": false,
		"scanDirectoryExclusions": []string{}, "repairExtensions": false,
		"convertToCbz": false, "emptyTrashAfterScan": false, "seriesCover": "FIRST",
		"hashFiles": false, "hashPages": false, "hashKoreader": false,
		"analyzeDimensions": false, "oneshotsDirectory": nil, "unavailable": false,
	}
}

func (k *komga) libraries(w http.ResponseWriter, _ *http.Request) {
	komgaWrite(w, []map[string]any{komgaLibrary()})
}

func (k *komga) library(w http.ResponseWriter, _ *http.Request) {
	komgaWrite(w, komgaLibrary())
}

func (k *komga) emptyPage(w http.ResponseWriter, r *http.Request) {
	komgaWrite(w, komgaPageOf([]any{}, 0, 0, 20, r.URL.Query().Get("unpaged") == "true"))
}

func (k *komga) emptyStrings(w http.ResponseWriter, _ *http.Request) {
	komgaWrite(w, []string{})
}

func (k *komga) emptyList(w http.ResponseWriter, _ *http.Request) {
	komgaWrite(w, []komgaAuthor{})
}

func komgaSeriesStatus(release string) string {
	switch strings.ToLower(strings.TrimSpace(release)) {
	case "finished", "completed", "complete", "ended":
		return "ENDED"
	case "hiatus":
		return "HIATUS"
	case "cancelled", "canceled", "abandoned":
		return "ABANDONED"
	default:
		return "ONGOING"
	}
}

func (k *komga) toSeries(t library.Title, read, inProgress, books int) komgaSeries {
	id := strconv.FormatInt(t.ID, 10)
	created := komgaTime(t.CreatedAt)
	modified := komgaTime(t.UpdatedAt)
	tags := t.ContentTags
	if tags == nil {
		tags = []string{}
	}
	unread := books - read - inProgress
	if unread < 0 {
		unread = 0
	}
	return komgaSeries{
		ID: id, LibraryID: komgaLibraryID, Name: t.DisplayTitle, URL: "",
		Created: created, LastModified: modified, FileLastModified: modified,
		BooksCount: books, BooksReadCount: read, BooksUnreadCount: unread,
		BooksInProgressCount: inProgress,
		Metadata: komgaSeriesMetadata{
			Status: komgaSeriesStatus(t.ReleaseStatus),
			Title:  t.DisplayTitle, TitleSort: t.DisplayTitle,
			Summary: "", ReadingDirection: "", Publisher: "", Language: "",
			Genres: []string{}, Tags: tags, SharingLabels: []string{},
			Created: created, LastModified: modified,
		},
		BooksMetadata: komgaBooksMetadata{
			Authors: []komgaAuthor{}, Tags: []string{}, Summary: "", SummaryNumber: "",
			Created: created, LastModified: modified,
		},
	}
}

// seriesCounts derives book counts from the title aggregates: books are the
// downloaded chapters, or the volumes for volume-only titles.
func seriesCounts(t library.Title) (books, read int) {
	books = int(t.DiscoveredCount - t.MissingCount - t.FailedCount)
	if books < 0 {
		books = 0
	}
	if books == 0 && t.VolumeCount > 0 {
		return int(t.VolumeCount), int(t.VolumeReadCount)
	}
	read = int(t.ReadCount)
	if read > books {
		read = books
	}
	return books, read
}

func (k *komga) seriesList(w http.ResponseWriter, r *http.Request) {
	titles, err := k.svc.ListTitles(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	titles = filterRestrictedTitles(r.Context(), titles)
	q := r.URL.Query()

	if search := strings.TrimSpace(q.Get("search")); search != "" {
		kept := titles[:0]
		for _, t := range titles {
			if strings.Contains(strings.ToLower(t.DisplayTitle), strings.ToLower(search)) {
				kept = append(kept, t)
			}
		}
		titles = kept
	}
	if statuses := q.Get("status"); statuses != "" {
		want := map[string]bool{}
		for _, s := range strings.Split(statuses, ",") {
			want[s] = true
		}
		kept := titles[:0]
		for _, t := range titles {
			if want[komgaSeriesStatus(t.ReleaseStatus)] {
				kept = append(kept, t)
			}
		}
		titles = kept
	}
	if reads := q["read_status"]; len(reads) > 0 {
		want := map[string]bool{}
		for _, s := range reads {
			want[s] = true
		}
		kept := titles[:0]
		for _, t := range titles {
			books, read := seriesCounts(t)
			state := "IN_PROGRESS"
			switch {
			case read == 0:
				state = "UNREAD"
			case read >= books && books > 0:
				state = "READ"
			}
			if want[state] {
				kept = append(kept, t)
			}
		}
		titles = kept
	}

	field, desc := "metadata.titleSort", false
	if s := q.Get("sort"); s != "" {
		parts := strings.SplitN(s, ",", 2)
		field = parts[0]
		desc = len(parts) == 2 && parts[1] == "desc"
	}
	sort.SliceStable(titles, func(i, j int) bool {
		var less bool
		switch field {
		case "createdDate":
			less = titles[i].CreatedAt.Before(titles[j].CreatedAt)
		case "lastModifiedDate":
			less = titles[i].UpdatedAt.Before(titles[j].UpdatedAt)
		default:
			less = strings.ToLower(titles[i].DisplayTitle) < strings.ToLower(titles[j].DisplayTitle)
		}
		if desc {
			return !less
		}
		return less
	})

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 0 {
		page = 0
	}
	size, _ := strconv.Atoi(q.Get("size"))
	if size <= 0 {
		size = 20
	}
	unpaged := q.Get("unpaged") == "true"
	total := len(titles)
	window := titles
	if !unpaged {
		lo := page * size
		if lo > total {
			lo = total
		}
		hi := lo + size
		if hi > total {
			hi = total
		}
		window = titles[lo:hi]
	}
	out := make([]komgaSeries, 0, len(window))
	for _, t := range window {
		books, read := seriesCounts(t)
		out = append(out, k.toSeries(t, read, 0, books))
	}
	komgaWrite(w, komgaPageOf(out, total, page, size, unpaged))
}

func (k *komga) allowedTitle(w http.ResponseWriter, r *http.Request) (library.Title, bool) {
	id, err := parseInt64Path(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return library.Title{}, false
	}
	t, err := k.svc.GetTitle(r.Context(), id)
	if err != nil || !contentAllowed(r.Context(), t.IsAdult, t.ContentTags) {
		writeError(w, http.StatusNotFound, "series not found")
		return library.Title{}, false
	}
	return t, true
}

// seriesBookList returns a title's books: downloaded chapters, or volumes
// when the title has no chapters, sorted by numberSort.
func (k *komga) seriesBookList(r *http.Request, t library.Title) []komgaBook {
	ctx := r.Context()
	out := []komgaBook{}
	statuses, _ := k.svc.TitleReadStatuses(ctx, t.ID)
	for _, st := range statuses {
		if st.Downloaded && st.OutputFile != "" {
			out = append(out, k.chapterBook(t, st))
		}
	}
	if len(out) == 0 {
		vols, _ := k.svc.Volumes(ctx, t.ID)
		for _, v := range vols {
			if v.File != "" {
				out = append(out, k.volumeBook(t, v))
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Metadata.NumberSort < out[j].Metadata.NumberSort
	})
	return out
}

func chapterNumberSort(ch library.ChapterReadStatus) float64 {
	if f, err := strconv.ParseFloat(ch.Label, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
		return f
	}
	return float64(ch.NumberMain)
}

func (k *komga) chapterBook(t library.Title, ch library.ChapterReadStatus) komgaBook {
	id := "c" + strconv.FormatInt(ch.ID, 10)
	created := komgaTime(ch.DiscoveredAt)
	modified := komgaTime(ch.UpdatedAt)
	name := "Ch " + ch.Label
	if ch.Title != "" {
		name += " · " + ch.Title
	}
	var progress *komgaReadProgress
	if ch.Completed || ch.LastPage > 0 {
		at := komgaTime(t.UpdatedAt)
		if ch.LastReadAt != nil {
			at = komgaTime(*ch.LastReadAt)
		}
		page := ch.LastPage
		if page < 1 {
			page = 1
		}
		progress = &komgaReadProgress{
			Page: page, Completed: ch.Completed,
			ReadDate: at, Created: at, LastModified: at,
		}
	}
	return komgaBook{
		ID: id, SeriesID: strconv.FormatInt(t.ID, 10), SeriesTitle: t.DisplayTitle,
		LibraryID: komgaLibraryID, Name: name, URL: "",
		Number: ch.NumberMain, Created: created, LastModified: modified,
		FileLastModified: modified, SizeBytes: ch.Bytes, Size: util.Human(ch.Bytes),
		Media: komgaMedia{
			Status: "READY", MediaType: "application/zip", PagesCount: ch.Pages,
			MediaProfile: "DIVINA",
		},
		Metadata: komgaBookMetadata{
			Title: name, Summary: "", Number: ch.Label,
			NumberSort: chapterNumberSort(ch),
			Authors:    []komgaAuthor{}, Tags: []string{},
			Created: created, LastModified: modified,
		},
		ReadProgress: progress,
	}
}

func (k *komga) volumeBook(t library.Title, v library.Volume) komgaBook {
	id := "v" + strconv.FormatInt(v.ID, 10)
	stamp := komgaTime(t.UpdatedAt)
	name := "Vol " + strconv.FormatFloat(v.Number, 'f', -1, 64)
	if v.Name != "" {
		name += " · " + v.Name
	}
	var progress *komgaReadProgress
	if v.Read || v.LastPage > 0 {
		page := v.LastPage
		if page < 1 {
			page = 1
		}
		progress = &komgaReadProgress{
			Page: page, Completed: v.Read,
			ReadDate: stamp, Created: stamp, LastModified: stamp,
		}
	}
	return komgaBook{
		ID: id, SeriesID: strconv.FormatInt(t.ID, 10), SeriesTitle: t.DisplayTitle,
		LibraryID: komgaLibraryID, Name: name, URL: "",
		Number: int(v.Number), Created: stamp, LastModified: stamp,
		FileLastModified: stamp, SizeBytes: v.Bytes, Size: util.Human(v.Bytes),
		Media: komgaMedia{
			Status: "READY", MediaType: "application/zip", PagesCount: v.Pages,
			MediaProfile: "DIVINA",
		},
		Metadata: komgaBookMetadata{
			Title: name, Summary: "",
			Number:     strconv.FormatFloat(v.Number, 'f', -1, 64),
			NumberSort: v.Number,
			Authors:    []komgaAuthor{}, Tags: []string{},
			Created: stamp, LastModified: stamp,
		},
		ReadProgress: progress,
	}
}

func (k *komga) seriesGet(w http.ResponseWriter, r *http.Request) {
	t, ok := k.allowedTitle(w, r)
	if !ok {
		return
	}
	books := k.seriesBookList(r, t)
	read, inProgress := 0, 0
	for _, b := range books {
		switch {
		case b.ReadProgress != nil && b.ReadProgress.Completed:
			read++
		case b.ReadProgress != nil:
			inProgress++
		}
	}
	komgaWrite(w, k.toSeries(t, read, inProgress, len(books)))
}

func (k *komga) seriesBooks(w http.ResponseWriter, r *http.Request) {
	t, ok := k.allowedTitle(w, r)
	if !ok {
		return
	}
	books := k.seriesBookList(r, t)
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 0 {
		page = 0
	}
	size, _ := strconv.Atoi(q.Get("size"))
	if size <= 0 {
		size = 20
	}
	unpaged := q.Get("unpaged") == "true"
	total := len(books)
	window := books
	if !unpaged {
		lo := page * size
		if lo > total {
			lo = total
		}
		hi := lo + size
		if hi > total {
			hi = total
		}
		window = books[lo:hi]
	}
	komgaWrite(w, komgaPageOf(window, total, page, size, unpaged))
}

func (k *komga) seriesThumbnail(w http.ResponseWriter, r *http.Request) {
	t, ok := k.allowedTitle(w, r)
	if !ok {
		return
	}
	serveTitleCover(w, r, k.svc, t.ID)
}

// bookRef resolves a "c<id>"/"v<id>" book id to its CBZ, page count, and
// title, enforcing content guards.
type bookRef struct {
	volume  bool
	id      int64
	titleID int64
	file    string
	pages   int
}

func (k *komga) resolveBook(r *http.Request) (bookRef, error) {
	raw := r.PathValue("id")
	if len(raw) < 2 {
		return bookRef{}, fmt.Errorf("invalid id")
	}
	id, err := strconv.ParseInt(raw[1:], 10, 64)
	if err != nil {
		return bookRef{}, fmt.Errorf("invalid id")
	}
	ctx := r.Context()
	switch raw[0] {
	case 'c':
		st, err := k.svc.ChapterReadStatus(ctx, id)
		if err != nil || !titleAllowed(ctx, k.svc, st.TitleID) {
			return bookRef{}, fmt.Errorf("not found")
		}
		if !st.Downloaded || st.OutputFile == "" {
			return bookRef{}, fmt.Errorf("not found")
		}
		return bookRef{id: id, titleID: st.TitleID, file: st.OutputFile, pages: st.Pages}, nil
	case 'v':
		v, err := k.svc.GetVolume(ctx, id)
		if err != nil || !titleAllowed(ctx, k.svc, v.TitleID) || v.File == "" {
			return bookRef{}, fmt.Errorf("not found")
		}
		return bookRef{volume: true, id: id, titleID: v.TitleID, file: v.File, pages: v.Pages}, nil
	}
	return bookRef{}, fmt.Errorf("invalid id")
}

func (k *komga) bookGet(w http.ResponseWriter, r *http.Request) {
	ref, err := k.resolveBook(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	t, err := k.svc.GetTitle(r.Context(), ref.titleID)
	if err != nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	if ref.volume {
		v, err := k.svc.GetVolume(r.Context(), ref.id)
		if err != nil {
			writeError(w, http.StatusNotFound, "book not found")
			return
		}
		komgaWrite(w, k.volumeBook(t, v))
		return
	}
	st, err := k.svc.ChapterReadStatus(r.Context(), ref.id)
	if err != nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	komgaWrite(w, k.chapterBook(t, st))
}

func (k *komga) bookPages(w http.ResponseWriter, r *http.Request) {
	ref, err := k.resolveBook(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	zr, err := zip.OpenReader(ref.file)
	if err != nil {
		writeError(w, http.StatusNotFound, "archive not available")
		return
	}
	defer zr.Close()
	entries := util.CBZImageEntries(zr.File)
	out := make([]komgaPageDto, 0, len(entries))
	for i, e := range entries {
		mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(e.Name)))
		if mt == "" {
			mt = "image/jpeg"
		}
		out = append(out, komgaPageDto{Number: i + 1, FileName: filepath.Base(e.Name), MediaType: mt})
	}
	komgaWrite(w, out)
}

func (k *komga) bookPage(w http.ResponseWriter, r *http.Request) {
	ref, err := k.resolveBook(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	page, err := strconv.Atoi(r.PathValue("page"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid page")
		return
	}
	if r.URL.Query().Get("zero_based") == "true" {
		page++
	}
	if page < 1 {
		writeError(w, http.StatusBadRequest, "invalid page")
		return
	}
	k.servePage(w, r, ref, page)
}

func (k *komga) servePage(w http.ResponseWriter, r *http.Request, ref bookRef, page int) {
	r.SetPathValue("id", strconv.FormatInt(ref.id, 10))
	r.SetPathValue("page", strconv.Itoa(page))
	if ref.volume {
		serveVolumePage(w, r, k.svc)
		return
	}
	serveChapterPage(w, r, k.svc)
}

func (k *komga) bookThumbnail(w http.ResponseWriter, r *http.Request) {
	ref, err := k.resolveBook(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	k.servePage(w, r, ref, 1)
}

func (k *komga) bookFile(w http.ResponseWriter, r *http.Request) {
	ref, err := k.resolveBook(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	serveArchive(w, r, ref.file)
}

func (k *komga) bookReadProgress(w http.ResponseWriter, r *http.Request) {
	ref, err := k.resolveBook(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	var body struct {
		Page      *int  `json:"page"`
		Completed *bool `json:"completed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx := r.Context()
	completed := body.Completed != nil && *body.Completed
	if body.Page != nil && *body.Page < 1 {
		writeError(w, http.StatusBadRequest, "page must be positive")
		return
	}
	if body.Page != nil && !completed && *body.Page >= ref.pages && ref.pages > 0 {
		completed = true // Komga derives completion from position
	}
	err = nil
	switch {
	case ref.volume && completed:
		err = k.svc.SetVolumeRead(ctx, ref.id, true)
	case ref.volume && body.Page != nil:
		_, err = k.svc.MarkVolumePageRead(ctx, ref.id, *body.Page, ref.pages)
	case completed:
		_, err = k.svc.MarkChapterRead(ctx, ref.id)
	case body.Page != nil:
		_, err = k.svc.MarkPageRead(ctx, ref.id, *body.Page, ref.pages)
	default:
		writeError(w, http.StatusBadRequest, "page or completed required")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if completed {
		k.svc.PushAniListEntry(ctx, auth.UserID(ctx), ref.titleID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (k *komga) bookReadProgressDelete(w http.ResponseWriter, r *http.Request) {
	ref, err := k.resolveBook(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "book not found")
		return
	}
	ctx := r.Context()
	if ref.volume {
		_ = k.svc.SetVolumeRead(ctx, ref.id, false)
	} else {
		_, _ = k.svc.MarkChapterUnread(ctx, ref.id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// tachiyomiProgress serves Mihon's enhanced-tracker view of a series:
// lastReadContinuousNumberSort is the highest numberSort with every book up
// to it read.
func (k *komga) tachiyomiProgress(w http.ResponseWriter, r *http.Request) {
	t, ok := k.allowedTitle(w, r)
	if !ok {
		return
	}
	books := k.seriesBookList(r, t)
	read, inProgress := 0, 0
	continuous, maxSort := 0.0, 0.0
	streak := true
	for _, b := range books {
		if b.Metadata.NumberSort > maxSort {
			maxSort = b.Metadata.NumberSort
		}
		done := b.ReadProgress != nil && b.ReadProgress.Completed
		if done {
			read++
		} else if b.ReadProgress != nil {
			inProgress++
		}
		if streak && done {
			continuous = b.Metadata.NumberSort
		} else {
			streak = false
		}
	}
	unread := len(books) - read - inProgress
	komgaWrite(w, map[string]any{
		"booksCount":                   len(books),
		"booksReadCount":               read,
		"booksUnreadCount":             unread,
		"booksInProgressCount":         inProgress,
		"lastReadContinuousNumberSort": continuous,
		"maxNumberSort":                maxSort,
	})
}

func (k *komga) tachiyomiProgressPut(w http.ResponseWriter, r *http.Request) {
	t, ok := k.allowedTitle(w, r)
	if !ok {
		return
	}
	var body struct {
		LastBookNumberSortRead float64 `json:"lastBookNumberSortRead"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	ctx := r.Context()
	for _, b := range k.seriesBookList(r, t) {
		if float32(b.Metadata.NumberSort) > float32(body.LastBookNumberSortRead) {
			continue
		}
		if b.ReadProgress != nil && b.ReadProgress.Completed {
			continue
		}
		if strings.HasPrefix(b.ID, "v") {
			id, _ := strconv.ParseInt(b.ID[1:], 10, 64)
			_ = k.svc.SetVolumeRead(ctx, id, true)
		} else {
			id, _ := strconv.ParseInt(b.ID[1:], 10, 64)
			_, _ = k.svc.MarkChapterRead(ctx, id)
		}
	}
	k.svc.PushAniListEntry(ctx, auth.UserID(ctx), t.ID)
	w.WriteHeader(http.StatusNoContent)
}
