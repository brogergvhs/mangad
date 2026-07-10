package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/service"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/util"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type webUI struct {
	svc  *service.JobService
	tmpl *template.Template
	kick func() // start the job runner now, non-blocking
}

type pageData struct {
	Title, Nav string
	Content    template.HTML
}
type readerView struct {
	PrevURL      string
	Title        library.Title
	Manifest     readerManifestResponse
	ManifestJSON template.JS // consumed by the reader script to build the strip
	Empty        string
	NextURL      string
	PagePosition string
}
type dashData struct {
	Titles     []library.Title
	Sources    []sources.Source
	Health     healthView
	TotalBytes int64
	TotalPages int64
	TotalChaps int64
}
type libraryView struct {
	Controls libraryControls
	Table    tableData
}
type libraryControls struct {
	Q        string
	Monitor  string
	Source   string
	Progress string
	Sort     string
	Dir      string
}
type healthView struct {
	Services []service.ServiceHealth
	Interval string // HTMX poll interval, e.g. "60s"
}
type activityView struct {
	Title         library.Title
	Manga         catalog.Manga
	ChaptersTable tableData
	Sources       matchView
	LinkedSources []linkedSourceView // sources already linked to this title
	SingleSources []sources.Source   // single-manga sources selectable for linking
	LinkSources   []sources.Source   // searchable sources for specifying a page URL
	RefreshEvery  string             // effective global refresh cadence
	Running       map[string]bool    // job type -> active (for button locking)
	ActiveLabel   string
	Failed        bool
	Error         string
	ReadLabel     string
}
type sourceRowView struct {
	Source sources.Source
	Active bool
}
type sourceProbeView struct {
	TitleID  int64
	SourceID string
	URL      string
	Result   service.SourceTestResult
}
type linkedSourceView struct {
	Name     string
	SourceID string
	URL      string
	Active   bool // the source used for refresh/download
}
type matchView struct {
	DomID     string
	PollURL   string
	Matches   []catalog.Match
	Active    bool
	Failed    bool
	Error     string
	TitleID   int64  // link target for "Use this source"
	LinkedURL string // the title's current source URL (marked as linked)
}

func titleSourceView(title library.Title, active, failed bool, msg string, matches []catalog.Match) matchView {
	return matchView{
		DomID:   fmt.Sprintf("sources-%d", title.ID),
		PollURL: fmt.Sprintf("/ui/library/%d/sources", title.ID),
		Active:  active, Failed: failed, Error: msg, Matches: matches,
		TitleID: title.ID, LinkedURL: title.SourceURL,
	}
}

type settingsView struct {
	Fields []settingField
}
type settingField struct {
	Key, Label, Desc, Value string
}

// settingMeta maps a technical setting key to a human label and description.
func settingMeta(key string) (label, desc string) {
	switch key {
	case service.SettingServeRefreshEvery:
		return "Check for new chapters", "How often each tracked manga's source is checked for newly released chapters (e.g. 1h, 30m)."
	case service.SettingServeScanEvery:
		return "Re-scan downloaded files", "How often the download folders are re-checked to reconcile which chapters are already on disk."
	case service.SettingServeDownloadEvery:
		return "Download missing chapters", "How often missing chapters of monitored manga are downloaded automatically."
	case service.SettingServeRunEvery:
		return "Background task interval", "How often the background worker wakes to run queued jobs. Lower is more responsive, higher is less busy."
	case service.SettingBrowserSolverEnabled:
		return "Use a browser solver for protected sites", "Enable a headless browser / Cloudflare solver for sources that block plain requests (true or false)."
	case service.SettingBrowserSolverProvider:
		return "Solver type", "Which solver to use for protected sites (e.g. flaresolverr)."
	case service.SettingBrowserSolverEndpoint:
		return "Solver address", "The URL where your solver (e.g. FlareSolverr) is reachable."
	case service.SettingBrowserSolverTimeoutSeconds:
		return "Solver timeout (seconds)", "How long to wait for the solver to load a page before giving up."
	case service.SettingBrowserDownloaderEnabled:
		return "Use browser image downloader", "Enable the Selenium browser worker fallback for sources whose images only load in a browser (true or false)."
	case service.SettingBrowserDownloaderEndpoint:
		return "Browser downloader address", "The URL where the browser downloader worker is reachable."
	case service.SettingBrowserDownloaderTimeoutSeconds:
		return "Browser downloader timeout (seconds)", "How long to wait for browser-captured chapter downloads before giving up."
	case service.SettingSourceRegistryURL:
		return "Extra source list URL", "Optional URL to load additional scraper definitions from. Leave blank to use built-in sources."
	case service.SettingJobsMaxAttempts:
		return "Job retry limit", "How many times a failed background job (refresh, scan) is retried before it is given up."
	case service.SettingJobsTimeout:
		return "Job stall limit", "How long a background job may go without making progress before it is aborted (e.g. 10m). Jobs that keep downloading run as long as they need."
	case service.SettingJobsWorkers:
		return "Job worker count", "How many background jobs can run at the same time. Jobs for the same manga still run one at a time."
	case service.SettingDownloadsMaxAttempts:
		return "Download retry limit", "How many times a failed chapter download is retried before giving up."
	case service.SettingServicesHealthInterval:
		return "Service health check interval", "How often the dashboard re-checks that FlareSolverr and the browser downloader are reachable (e.g. 60s, 2m)."
	}
	return key, ""
}

type toastView struct {
	OK  bool
	Msg string
}

// registerUI mounts the server-rendered UI and its HTMX endpoints on mux.
func registerUI(mux *http.ServeMux, svc *service.JobService, runJobs func(context.Context) (service.RunSummary, error)) {
	u := &webUI{svc: svc, kick: func() {
		if runJobs != nil {
			go func() { _, _ = runJobs(context.Background()) }()
		}
	}}
	u.tmpl = template.Must(template.New("").Funcs(u.funcs()).ParseFS(templateFS, "templates/*.html"))

	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))

	mux.HandleFunc("GET /{$}", u.dashboard)
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) { u.page(w, r, "search", "Search", nil) })
	mux.HandleFunc("GET /library", u.libraryPage)
	mux.HandleFunc("GET /library/{id}", u.titlePage)
	mux.HandleFunc("GET /reader/{id}", u.readerPage)
	mux.HandleFunc("GET /import", u.importPage)
	mux.HandleFunc("GET /sources", u.sourcesPage)
	mux.HandleFunc("GET /settings", u.settingsPage)

	mux.HandleFunc("POST /ui/search", u.search)
	mux.HandleFunc("POST /ui/library/add", u.addToLibrary)
	mux.HandleFunc("POST /ui/library/{id}/refresh", u.libAction(jobs.TypeRefreshTitle, "refreshing"))
	mux.HandleFunc("POST /ui/library/{id}/download", u.libAction(jobs.TypeDownloadMissing, "downloading"))
	mux.HandleFunc("POST /ui/library/{id}/scan", u.libAction(jobs.TypeScanDownloads, "scanning"))
	mux.HandleFunc("GET /ui/library/{id}/activity", u.libActivity)
	mux.HandleFunc("POST /ui/library/{id}/monitored", u.libMonitored)
	mux.HandleFunc("POST /ui/library/{id}/refresh-interval", u.libRefreshInterval)
	mux.HandleFunc("POST /ui/library/{id}/remove", u.libRemove)
	mux.HandleFunc("POST /ui/library/{id}/find-sources", u.findSources)
	mux.HandleFunc("GET /ui/library/{id}/sources", u.titleSources)
	mux.HandleFunc("GET /ui/library/{id}/chapters", u.chaptersTable)
	mux.HandleFunc("POST /ui/library/{id}/chapters/{chapterID}/read", u.chapterRead(true))
	mux.HandleFunc("POST /ui/library/{id}/chapters/{chapterID}/unread", u.chapterRead(false))
	mux.HandleFunc("POST /ui/library/{id}/chapters/range", u.chapterRangeRead)
	mux.HandleFunc("POST /ui/library/{id}/link", u.linkSource)
	mux.HandleFunc("POST /ui/library/{id}/link-source", u.linkSourceByID)
	mux.HandleFunc("POST /ui/library/{id}/verify-source", u.srcVerifyURL)
	mux.HandleFunc("POST /ui/library/{id}/link-source-url", u.linkSourceURL)
	mux.HandleFunc("POST /ui/library/{id}/unlink-source", u.unlinkSource)
	mux.HandleFunc("POST /ui/import/{folder}/search", u.importSearch)
	mux.HandleFunc("POST /ui/import", u.importDo)
	mux.HandleFunc("POST /ui/sources/{id}/verify", u.srcVerify)
	mux.HandleFunc("GET /ui/sources/{id}/edit", u.srcEdit)
	mux.HandleFunc("POST /ui/sources/{id}/edit", u.srcEditSave)
	mux.HandleFunc("GET /ui/sources/{id}/row", u.srcRow)
	mux.HandleFunc("POST /ui/sources/sync", u.srcSync)
	mux.HandleFunc("POST /ui/sources/test", u.srcTest)
	mux.HandleFunc("POST /ui/sources/custom", u.srcAddCustom)
	mux.HandleFunc("GET /ui/library/table", u.libraryTable)
	mux.HandleFunc("GET /ui/jobs/table", u.jobsTable)
	mux.HandleFunc("POST /ui/jobs/{id}/cancel", u.jobCancel)
	mux.HandleFunc("GET /ui/health", u.health)
	mux.HandleFunc("PUT /ui/settings", u.settingsSave)
}

// --- rendering ---

func (u *webUI) page(w http.ResponseWriter, r *http.Request, content, title string, data any) {
	var buf bytes.Buffer
	if err := u.tmpl.ExecuteTemplate(&buf, content, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := u.tmpl.ExecuteTemplate(w, "layout.html", pageData{Title: title, Nav: navFor(r.URL.Path), Content: template.HTML(buf.String())}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (u *webUI) readerLayout(w http.ResponseWriter, title string, data readerView) {
	if err := u.tmpl.ExecuteTemplate(w, "reader_layout.html", pageData{Title: title, Content: u.renderToHTML("reader", data)}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (u *webUI) frag(w http.ResponseWriter, name string, data any) {
	if err := u.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (u *webUI) fail(w http.ResponseWriter, err error) {
	// htmx 2 does not swap 4xx bodies, and mutating controls use
	// hx-swap="none", so route the error toast to a global container with a
	// 200 status — otherwise failures are silently invisible.
	w.Header().Set("HX-Retarget", "#toast")
	w.Header().Set("HX-Reswap", "innerHTML")
	u.frag(w, "toast", toastView{Msg: err.Error()})
}

// --- pages ---

func (u *webUI) dashboard(w http.ResponseWriter, r *http.Request) {
	titles, _ := u.svc.ListTitles(r.Context())
	srcs, _ := u.svc.ListSources(r.Context())
	data := dashData{Titles: titles, Sources: srcs, Health: u.healthView(r.Context())}
	for _, t := range titles {
		data.TotalBytes += t.SizeBytes
		data.TotalPages += t.Pages
		data.TotalChaps += t.DiscoveredCount
	}
	u.page(w, r, "dashboard", "Dashboard", data)
}

func (u *webUI) health(w http.ResponseWriter, r *http.Request) {
	u.frag(w, "servicesHealth", u.healthView(r.Context()))
}

func (u *webUI) healthView(ctx context.Context) healthView {
	return healthView{
		Services: u.svc.ServicesHealth(ctx),
		Interval: u.svc.Setting(ctx, service.SettingServicesHealthInterval, service.SettingDefault(service.SettingServicesHealthInterval)),
	}
}

const (
	libraryPerPage = 20
	jobsPerPage    = 15
)

func (u *webUI) libraryPage(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	u.page(w, r, "library", "Library", libraryView{
		Controls: libraryControlsFrom(values),
		Table:    u.buildLibraryTable(r.Context(), values),
	})
}

func (u *webUI) libraryTable(w http.ResponseWriter, r *http.Request) {
	u.frag(w, "table", u.buildLibraryTable(r.Context(), r.URL.Query()))
}

func (u *webUI) readerPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	progress, err := u.svc.ReaderProgress(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	currentID, _ := strconv.ParseInt(r.URL.Query().Get("chapter"), 10, 64)
	manifest, prevID, nextID := readerManifestWindow(progress, currentID)
	data := readerView{Title: progress.Title, Manifest: manifest, PagePosition: initialReaderPosition(manifest)}
	if len(data.Manifest.Chapters) == 0 {
		data.Empty = "No downloaded chapters are available to read yet."
	}
	if prevID > 0 {
		data.PrevURL = fmt.Sprintf("/reader/%d?chapter=%d", progress.ID, prevID)
	}
	if nextID > 0 {
		data.NextURL = fmt.Sprintf("/reader/%d?chapter=%d", progress.ID, nextID)
	}
	if raw, err := json.Marshal(data.Manifest); err == nil {
		data.ManifestJSON = template.JS(raw)
	}
	u.readerLayout(w, progress.DisplayTitle, data)
}

func initialReaderPosition(manifest readerManifestResponse) string {
	if manifest.ResumePage <= 0 {
		return ""
	}
	for _, chapter := range manifest.Chapters {
		if chapter.ID == manifest.ResumeChapterID && chapter.PageCount > 0 {
			return fmt.Sprintf("%d/%d", manifest.ResumePage, chapter.PageCount)
		}
	}
	return strconv.Itoa(manifest.ResumePage)
}

func (u *webUI) buildLibraryTable(ctx context.Context, values url.Values) tableData {
	page, _, _ := tableParams(values, libraryPerPage)
	controls := libraryControlsFrom(values)
	titles, _ := u.svc.ListTitles(ctx)
	allCount := len(titles)
	titles = filterTitles(titles, controls)
	sortTitles(titles, controls.Sort, controls.Dir)
	pageTitles, total := paginate(titles, page, libraryPerPage)
	js := u.jobs(ctx)
	empty := "Nothing in your library yet — add manga from Search or Import a collection."
	if allCount > 0 {
		empty = "No manga match the current search or filters."
	}

	t := tableData{
		ID: "library-table", BaseURL: "/ui/library/table",
		Page: page, PerPage: libraryPerPage, Total: total, Sort: controls.Sort, Dir: controls.Dir,
		Params: libraryTableParams(values),
		Empty:  empty,
		Columns: []tableColumn{
			{Label: ""},
			{Label: "Title"},
			{Label: "Chapters"},
			{Label: "Monitor"},
			{Label: "Status"},
		},
	}
	for _, tl := range pageTitles {
		running, label, failed, msg := titleActivityFrom(js, tl)
		if len(running) > 0 {
			t.Poll = true
		}
		view := activityView{Title: tl, Running: running, ActiveLabel: label, Failed: failed, Error: msg}
		var detail template.HTML
		if tl.CatalogMangaID != nil {
			if m, err := u.svc.GetManga(ctx, *tl.CatalogMangaID); err == nil {
				detail = u.renderToHTML("mangaDetail", m)
			}
		}
		t.Rows = append(t.Rows, tableRow{
			ID: strconv.FormatInt(tl.ID, 10),
			Cells: []template.HTML{
				u.renderToHTML("cellCover", tl.CoverImage),
				u.renderToHTML("cellTitle", view),
				u.renderToHTML("progressBar", tl),
				u.renderToHTML("monitorToggle", tl),
				u.renderToHTML("cellActivity", view),
			},
			Detail: detail,
		})
	}
	return t
}

func libraryControlsFrom(values url.Values) libraryControls {
	c := libraryControls{
		Q:        strings.TrimSpace(values.Get("q")),
		Monitor:  values.Get("monitor"),
		Source:   values.Get("source"),
		Progress: values.Get("progress"),
		Sort:     values.Get("sort"),
		Dir:      values.Get("dir"),
	}
	if c.Monitor == "" {
		c.Monitor = "all"
	}
	if c.Source == "" {
		c.Source = "all"
	}
	if c.Progress == "" {
		c.Progress = "all"
	}
	if c.Sort == "" {
		c.Sort = "added"
		if c.Dir == "" {
			c.Dir = "desc" // newest additions first by default
		}
	}
	if c.Dir != "desc" {
		c.Dir = "asc"
	}
	return c
}

func libraryTableParams(values url.Values) url.Values {
	out := url.Values{}
	for _, key := range []string{"q", "monitor", "source", "progress", "sort", "dir"} {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			out.Set(key, value)
		}
	}
	return out
}

func filterTitles(titles []library.Title, c libraryControls) []library.Title {
	q := strings.ToLower(c.Q)
	out := titles[:0]
	for _, title := range titles {
		if q != "" && !strings.Contains(strings.ToLower(title.DisplayTitle+" "+title.SourceID+" "+title.ReleaseStatus), q) {
			continue
		}
		switch c.Monitor {
		case "on":
			if !title.Monitored {
				continue
			}
		case "off":
			if title.Monitored {
				continue
			}
		}
		switch c.Source {
		case "linked":
			if !strings.HasPrefix(title.SourceURL, "http") {
				continue
			}
		case "unlinked":
			if strings.HasPrefix(title.SourceURL, "http") {
				continue
			}
		case "imported":
			if !strings.HasPrefix(title.SourceURL, "local:") {
				continue
			}
		}
		switch c.Progress {
		case "missing":
			if title.MissingCount == 0 {
				continue
			}
		case "complete":
			if title.DiscoveredCount == 0 || title.MissingCount != 0 {
				continue
			}
		case "empty":
			if title.DiscoveredCount != 0 {
				continue
			}
		}
		out = append(out, title)
	}
	return out
}

func (u *webUI) jobCancel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		u.fail(w, fmt.Errorf("invalid job id"))
		return
	}
	if err := u.svc.CancelJob(r.Context(), id); err != nil {
		u.fail(w, err)
		return
	}
	u.jobsTable(w, r)
}

func (u *webUI) jobsTable(w http.ResponseWriter, r *http.Request) {
	page, key, dir := tableParams(r.URL.Query(), jobsPerPage)
	all, _ := u.svc.List(r.Context())
	sortJobs(all, key, dir)
	rows, total := paginate(all, page, jobsPerPage)

	t := tableData{
		ID: "jobs-table", BaseURL: "/ui/jobs/table",
		Page: page, PerPage: jobsPerPage, Total: total, Sort: key, Dir: dir,
		Poll: anyActive(all), Empty: "No jobs yet.",
		Columns: []tableColumn{
			{Label: "Job", SortKey: "type"},
			{Label: "Status"},
			{Label: "Attempts"},
			{Label: "When", SortKey: "updated"},
			{Label: ""},
		},
	}
	for _, j := range rows {
		cancel := template.HTML("")
		if active, _ := jobState(j.Status); active {
			cancel = u.renderToHTML("jobCancel", j)
		}
		t.Rows = append(t.Rows, tableRow{
			ID: strconv.FormatInt(j.ID, 10),
			Cells: []template.HTML{
				text(jobLabel(j.Type)),
				u.renderToHTML("jobStatusBadge", j),
				text(strconv.Itoa(j.Attempts)),
				text(since(j.UpdatedAt)),
				cancel,
			},
			Detail: u.renderToHTML("jobDetail", j),
		})
	}
	u.frag(w, "table", t)
}

func (u *webUI) titlePage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	title, err := u.svc.GetTitle(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	view := u.titleActivity(r.Context(), id)
	view.Title = title
	view.ReadLabel = "Read"
	if progress, err := u.svc.ReaderProgress(r.Context(), id); err == nil && progress.NextChapterID != 0 && progress.ReadPages > 0 {
		view.ReadLabel = "Continue reading"
	}
	view.RefreshEvery = u.svc.Setting(r.Context(), service.SettingServeRefreshEvery, service.SettingDefault(service.SettingServeRefreshEvery))
	view.ChaptersTable = u.buildChaptersTable(r.Context(), title, r.URL.Query())
	linked := u.linkedSourceIDs(r.Context(), id)
	view.LinkedSources = u.linkedSourceViews(r.Context(), title)
	view.Sources = u.sourceView(r.Context(), title, linked)
	view.SingleSources = filterSources(u.singleMangaSources(r.Context()), linked)
	view.LinkSources = filterSources(u.searchableSources(r.Context()), linked)
	if title.CatalogMangaID != nil {
		view.Manga, _ = u.svc.GetManga(r.Context(), *title.CatalogMangaID)
	}
	u.page(w, r, "title", title.DisplayTitle, view)
}

// singleMangaSources returns enabled sources flagged as single-manga.
func (u *webUI) singleMangaSources(ctx context.Context) []sources.Source {
	all, _ := u.svc.ListSources(ctx)
	var out []sources.Source
	for _, s := range all {
		if s.SingleManga && s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

// searchableSources returns enabled multi-manga sources, for specifying a page.
func (u *webUI) searchableSources(ctx context.Context) []sources.Source {
	all, _ := u.svc.ListSources(ctx)
	var out []sources.Source
	for _, s := range all {
		if s.Enabled && !s.SingleManga {
			out = append(out, s)
		}
	}
	return out
}

const chaptersPerPage = 25

func (u *webUI) chaptersTable(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.writeChaptersTable(w, r, id)
}

func (u *webUI) chapterRead(read bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		titleID, err := pathID(r)
		if err != nil {
			u.fail(w, err)
			return
		}
		chapterID, err := parseInt64Path(r, "chapterID")
		if err != nil {
			u.fail(w, err)
			return
		}
		if read {
			_, err = u.svc.MarkChapterRead(r.Context(), chapterID)
		} else {
			_, err = u.svc.MarkChapterUnread(r.Context(), chapterID)
		}
		if err != nil {
			u.fail(w, err)
			return
		}
		u.writeChaptersTable(w, r, titleID)
	}
}

func (u *webUI) chapterRangeRead(w http.ResponseWriter, r *http.Request) {
	titleID, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	switch r.FormValue("action") {
	case "read":
		_, err = u.svc.MarkChapterRangeRead(r.Context(), titleID, r.FormValue("from"), r.FormValue("to"))
	case "unread":
		_, err = u.svc.MarkChapterRangeUnread(r.Context(), titleID, r.FormValue("from"), r.FormValue("to"))
	default:
		err = fmt.Errorf("unknown read action")
	}
	if err != nil {
		u.fail(w, err)
		return
	}
	u.writeChaptersTable(w, r, titleID)
}

func (u *webUI) writeChaptersTable(w http.ResponseWriter, r *http.Request, titleID int64) {
	title, err := u.svc.GetTitle(r.Context(), titleID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u.frag(w, "table", u.buildChaptersTable(r.Context(), title, r.URL.Query()))
}

func (u *webUI) buildChaptersTable(ctx context.Context, title library.Title, values url.Values) tableData {
	page, key, dir := tableParams(values, chaptersPerPage)
	chs, _ := u.svc.TitleChapters(ctx, title.ID)
	sortChapters(chs, key, dir)
	rows, total := paginate(chs, page, chaptersPerPage)

	empty := "Link a source to discover chapters."
	if strings.HasPrefix(title.SourceURL, "http") {
		empty = "No chapters yet — refresh to discover them."
	}
	t := tableData{
		ID: "chapters-table", BaseURL: fmt.Sprintf("/ui/library/%d/chapters", title.ID),
		Page: page, PerPage: chaptersPerPage, Total: total, Sort: key, Dir: dir, Empty: empty,
		Columns: []tableColumn{
			{Label: "Chapter", SortKey: "number"},
			{Label: "Status", SortKey: "status"},
			{Label: "Pages"},
			{Label: "Size"},
			{Label: "Source"},
			{Label: "Read"},
		},
	}
	for _, c := range rows {
		pages, size := "—", "—"
		if c.Downloaded {
			pages, size = strconv.Itoa(c.Pages), util.Human(c.Bytes)
			if c.Pages == 0 {
				pages = "—"
			}
		}
		t.Rows = append(t.Rows, tableRow{
			ID: strconv.FormatInt(c.ID, 10),
			Cells: []template.HTML{
				u.renderToHTML("chapterName", c),
				u.renderToHTML("chapterStatus", c),
				text(pages),
				text(size),
				text(chapterSource(c.URL)),
				u.renderToHTML("chapterReadAction", c),
			},
		})
	}
	return t
}

// sourceView builds the candidate-source list for a title, excluding sources
// already linked.
func (u *webUI) sourceView(ctx context.Context, title library.Title, linked map[string]bool) matchView {
	if title.CatalogMangaID == nil {
		return titleSourceView(title, false, false, "", nil)
	}
	cid := *title.CatalogMangaID
	active, failed, msg := u.jobStateFor(ctx, jobs.TypeMatchSources, service.JobPayload{CatalogID: cid})
	if active {
		return titleSourceView(title, true, false, "", nil)
	}
	matches, _ := u.svc.ListMatches(ctx, cid)
	kept := matches[:0]
	for _, m := range matches {
		if !linked[m.SourceID] {
			kept = append(kept, m)
		}
	}
	return titleSourceView(title, false, failed, msg, kept)
}

// linkedSourceIDs is the set of source IDs already linked to a title.
func (u *webUI) linkedSourceIDs(ctx context.Context, titleID int64) map[string]bool {
	links, _ := u.svc.ListTitleSources(ctx, titleID)
	m := make(map[string]bool, len(links))
	for _, l := range links {
		if l.SourceID != "" {
			m[l.SourceID] = true
		}
	}
	return m
}

// linkedSourceViews resolves each linked source's display name and active state.
func (u *webUI) linkedSourceViews(ctx context.Context, title library.Title) []linkedSourceView {
	links, _ := u.svc.ListTitleSources(ctx, title.ID)
	out := make([]linkedSourceView, 0, len(links))
	for _, l := range links {
		name := l.SourceID
		if src, err := u.svc.GetSource(ctx, l.SourceID); err == nil && src.Name != "" {
			name = src.Name
		}
		out = append(out, linkedSourceView{Name: name, SourceID: l.SourceID, URL: l.URL, Active: l.URL == title.SourceURL})
	}
	return out
}

func filterSources(list []sources.Source, linked map[string]bool) []sources.Source {
	out := make([]sources.Source, 0, len(list))
	for _, s := range list {
		if !linked[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

func (u *webUI) sourcesPage(w http.ResponseWriter, r *http.Request) {
	srcs, err := u.svc.ListSources(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	u.page(w, r, "sources", "Sources", srcs)
}

func (u *webUI) settingsPage(w http.ResponseWriter, r *http.Request) {
	u.page(w, r, "settings", "Settings", u.settings(r.Context()))
}

// --- search & add ---

type searchResultView struct {
	catalog.Manga
	TitleID int64 // set when the manga is already in the library
}

func (u *webUI) search(w http.ResponseWriter, r *http.Request) {
	items, err := u.svc.SearchAniList(r.Context(), r.FormValue("q"), 10)
	if err != nil {
		u.fail(w, err)
		return
	}
	inLibrary, _ := u.svc.TitlesByProvider(r.Context(), catalog.AniListProvider)
	views := make([]searchResultView, len(items))
	for i, m := range items {
		views[i] = searchResultView{Manga: m, TitleID: inLibrary[m.ProviderID]}
	}
	u.frag(w, "searchResults", views)
}

func (u *webUI) addToLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.FormValue("provider_id"))
	if err != nil {
		u.fail(w, fmt.Errorf("invalid id"))
		return
	}
	title, err := u.svc.AddCatalogTitle(r.Context(), id)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	u.frag(w, "addedButton", title)
}

// --- library ---

func (u *webUI) libAction(typ, label string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			u.fail(w, err)
			return
		}
		title, err := u.svc.GetTitle(r.Context(), id)
		if err != nil {
			u.fail(w, err)
			return
		}
		if _, err := u.svc.Enqueue(r.Context(), typ, id, time.Now()); err != nil {
			u.fail(w, err)
			return
		}
		u.kick()
		view := u.titleActivity(r.Context(), id)
		view.Title = title
		if view.Running == nil {
			view.Running = map[string]bool{typ: true}
			view.ActiveLabel = label
		}
		u.frag(w, "titleActivity", view)
	}
}

func (u *webUI) libActivity(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	view := u.titleActivity(r.Context(), id)
	if len(view.Running) == 0 {
		// Job finished: reload so counts, progress, and the chapter list refresh.
		w.Header().Set("HX-Refresh", "true")
		return
	}
	title, err := u.svc.GetTitle(r.Context(), id)
	if err != nil {
		u.fail(w, err)
		return
	}
	view.Title = title
	u.frag(w, "titleActivity", view)
}

func (u *webUI) libRefreshInterval(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := u.svc.SetRefreshInterval(r.Context(), id, r.FormValue("interval")); err != nil {
		u.fail(w, err)
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/library/%d", id))
}

func (u *webUI) libMonitored(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	on := r.FormValue("on") == "true"
	if err := u.svc.SetMonitored(r.Context(), id, on); err != nil {
		u.fail(w, err)
		return
	}
	title, _ := u.svc.GetTitle(r.Context(), id)
	u.frag(w, "monitorToggle", title)
}

func (u *webUI) libRemove(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if _, err := u.svc.RemoveTitle(r.Context(), id); err != nil {
		u.fail(w, err)
		return
	}
	w.Header().Set("HX-Redirect", "/library")
	w.WriteHeader(http.StatusOK)
}

// --- import & source linking ---

type importPickerView struct {
	Folder  string
	Query   string
	Results []catalog.Manga
}

func (u *webUI) importPage(w http.ResponseWriter, r *http.Request) {
	cands, err := u.svc.ExploreDownloads(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	u.page(w, r, "import", "Import", cands)
}

func (u *webUI) importSearch(w http.ResponseWriter, r *http.Request) {
	folder := r.PathValue("folder")
	query := strings.TrimSpace(r.FormValue("q"))
	if query == "" {
		query = importQuery(folder)
	}
	items, err := u.svc.SearchAniList(r.Context(), query, 10)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "importPicker", importPickerView{Folder: folder, Query: query, Results: items})
}

func (u *webUI) importDo(w http.ResponseWriter, r *http.Request) {
	anilistID, err := strconv.Atoi(r.FormValue("anilist_id"))
	if err != nil {
		u.fail(w, fmt.Errorf("select an AniList match first"))
		return
	}
	title, err := u.svc.ImportFolder(r.Context(), r.FormValue("folder"), anilistID)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	w.Header().Set("HX-Redirect", fmt.Sprintf("/library/%d", title.ID))
}

func (u *webUI) findSources(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	title, err := u.svc.GetTitle(r.Context(), id)
	if err != nil {
		u.fail(w, err)
		return
	}
	if title.CatalogMangaID == nil {
		u.fail(w, fmt.Errorf("no catalog metadata to search from"))
		return
	}
	if _, err := u.svc.EnqueueCatalog(r.Context(), jobs.TypeMatchSources, *title.CatalogMangaID, time.Now()); err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	u.frag(w, "matches", titleSourceView(title, true, false, "", nil))
}

func (u *webUI) titleSources(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	title, err := u.svc.GetTitle(r.Context(), id)
	if err != nil {
		u.frag(w, "matches", matchView{DomID: fmt.Sprintf("sources-%d", id), PollURL: fmt.Sprintf("/ui/library/%d/sources", id)})
		return
	}
	u.frag(w, "matches", u.sourceView(r.Context(), title, u.linkedSourceIDs(r.Context(), id)))
}

func (u *webUI) unlinkSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := u.svc.UnlinkTitleSource(r.Context(), id, r.FormValue("url")); err != nil {
		u.fail(w, err)
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/library/%d", id))
}

func (u *webUI) linkSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	matchID, err := strconv.ParseInt(r.FormValue("match_id"), 10, 64)
	if err != nil {
		u.fail(w, fmt.Errorf("invalid match id"))
		return
	}
	if _, err := u.svc.LinkTitleSource(r.Context(), id, matchID); err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	w.Header().Set("HX-Redirect", fmt.Sprintf("/library/%d", id))
}

func (u *webUI) srcVerifyURL(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	rawURL := strings.TrimSpace(r.FormValue("url"))
	res, err := u.svc.VerifySourceURL(r.Context(), sourceID, rawURL)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "sourceProbe", sourceProbeView{TitleID: id, SourceID: sourceID, URL: rawURL, Result: res})
}

func (u *webUI) linkSourceURL(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if _, err := u.svc.LinkTitleSourceURL(r.Context(), id, r.FormValue("source_id"), r.FormValue("url")); err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	w.Header().Set("HX-Redirect", fmt.Sprintf("/library/%d", id))
}

func (u *webUI) linkSourceByID(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	sourceID := strings.TrimSpace(r.FormValue("source_id"))
	if sourceID == "" {
		u.fail(w, fmt.Errorf("pick a source"))
		return
	}
	if _, err := u.svc.LinkTitleToSource(r.Context(), id, sourceID); err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	w.Header().Set("HX-Redirect", fmt.Sprintf("/library/%d", id))
}

func importQuery(folder string) string {
	return strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(folder))
}

// --- sources ---

func (u *webUI) srcVerify(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := u.svc.EnqueueSource(r.Context(), id, time.Now()); err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	src, err := u.svc.GetSource(r.Context(), id)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "sourceRow", sourceRowView{Source: src, Active: true})
}

func (u *webUI) srcRow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := u.svc.GetSource(r.Context(), id)
	if err != nil {
		u.fail(w, err)
		return
	}
	active, _, _ := u.jobStateFor(r.Context(), jobs.TypeVerifySource, service.JobPayload{SourceID: id})
	u.frag(w, "sourceRow", sourceRowView{Source: src, Active: active})
}

func (u *webUI) srcEdit(w http.ResponseWriter, r *http.Request) {
	src, err := u.svc.GetSource(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "sourceEdit", src)
}

func (u *webUI) srcEditSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := u.svc.GetSource(r.Context(), id)
	if err != nil {
		u.fail(w, err)
		return
	}
	_ = r.ParseForm()
	p := src.Profile
	p.Name = strings.TrimSpace(r.FormValue("name"))
	p.BaseURL = strings.TrimSpace(r.FormValue("base_url"))
	p.SampleMangaURL = strings.TrimSpace(r.FormValue("manga_url"))
	p.SearchURL = strings.TrimSpace(r.FormValue("search_url"))
	p.Domains = splitList(r.FormValue("domains"))
	p.AllowedExtensions = splitList(r.FormValue("extensions"))
	p.SingleManga = formChecked(r, "single_manga")
	p.Enabled = formChecked(r, "enabled")
	// Saving stores a local override; built-in sync no longer clobbers it.
	if err := u.svc.ImportLocalSource(r.Context(), p); err != nil {
		u.fail(w, err)
		return
	}
	if err := u.svc.SetSourceMethods(r.Context(), id, r.FormValue("chapter_fetch"), r.FormValue("image_fetch")); err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	w.Header().Set("HX-Refresh", "true")
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (u *webUI) srcSync(w http.ResponseWriter, r *http.Request) {
	registry := u.svc.Setting(r.Context(), service.SettingSourceRegistryURL, "")
	if err := u.svc.SyncSources(r.Context(), registry); err != nil {
		u.fail(w, err)
		return
	}
	srcs, _ := u.svc.ListSources(r.Context())
	u.frag(w, "sourcesTable", srcs)
}

func (u *webUI) srcTest(w http.ResponseWriter, r *http.Request) {
	profile, solver, browser, err := customProfileFromForm(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	res, err := u.svc.TestSource(r.Context(), profile, solver, browser)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "sourceTest", res)
}

func (u *webUI) srcAddCustom(w http.ResponseWriter, r *http.Request) {
	profile, solver, browser, err := customProfileFromForm(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := u.svc.ImportLocalSource(r.Context(), profile); err != nil {
		u.fail(w, err)
		return
	}
	// Pin the fetch methods the user chose so verify/downloads honor them.
	chapterFetch, imageFetch := "", ""
	if solver {
		chapterFetch = sources.FetchSolver
	}
	if browser {
		imageFetch = sources.FetchBrowser
	}
	if err := u.svc.SetSourceMethods(r.Context(), profile.ID, chapterFetch, imageFetch); err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	w.Header().Set("HX-Refresh", "true")
}

// customProfileFromForm builds a local source profile from the add-source form.
func customProfileFromForm(r *http.Request) (sources.Profile, bool, bool, error) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	base := strings.TrimSpace(r.FormValue("base_url"))
	manga := strings.TrimSpace(r.FormValue("manga_url"))
	if base == "" || manga == "" {
		return sources.Profile{}, false, false, fmt.Errorf("site base URL and manga page URL are required")
	}
	var exts []string
	for _, e := range r.Form["ext"] {
		if e = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(e)), "."); e != "" {
			exts = append(exts, e)
		}
	}
	host := ""
	if u, err := url.Parse(base); err == nil {
		host = u.Host
	}
	id := slugify(name)
	if id == "" {
		id = slugify(host)
	}
	if id == "" {
		return sources.Profile{}, false, false, fmt.Errorf("a name is required")
	}
	name = orDefault(name, host)
	var domains []string
	if host != "" {
		domains = []string{host}
	}
	solver := formChecked(r, "solver")
	browser := formChecked(r, "browser")
	return sources.Profile{
		ID: id, Name: name, Domains: domains,
		BaseURL: base, SampleMangaURL: manga, Scraper: "generic",
		AllowedExtensions: exts, MinChapters: 1,
		RequiresBrowserSolver: solver, RequiresBrowserDownload: browser,
		SingleManga: formChecked(r, "single_manga"), Enabled: true,
	}, solver, browser, nil
}

func formChecked(r *http.Request, name string) bool {
	switch r.FormValue(name) {
	case "on", "true", "1":
		return true
	}
	return false
}

var reNonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	return strings.Trim(reNonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// --- settings ---

func (u *webUI) settingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	for _, key := range service.SettingKeys() {
		value := strings.TrimSpace(r.FormValue(key))
		// Leaving a field at its default clears any override so env/config wins
		// (avoids the pre-filled default clobbering e.g. a solver endpoint set
		// via environment).
		if value == "" || value == service.SettingDefault(key) {
			if err := u.svc.ClearSetting(r.Context(), key); err != nil {
				u.fail(w, err)
				return
			}
			continue
		}
		if err := service.ValidateSetting(key, value); err != nil {
			u.fail(w, err)
			return
		}
		if err := u.svc.SetSetting(r.Context(), key, value); err != nil {
			u.fail(w, err)
			return
		}
	}
	u.frag(w, "toast", toastView{OK: true, Msg: "Saved ✓"})
}

// --- helpers ---

func (u *webUI) settings(ctx context.Context) settingsView {
	cfg, _, _ := u.svc.RuntimeConfig(ctx) // effective config (env + settings merged)
	keys := service.SettingKeys()
	fields := make([]settingField, 0, len(keys))
	for _, key := range keys {
		label, desc := settingMeta(key)
		value := u.svc.Setting(ctx, key, service.SettingDefault(key))
		if cfg != nil {
			if eff, ok := effectiveSetting(cfg, key); ok {
				value = eff // show what's actually in effect (e.g. env-set endpoint)
			}
		}
		fields = append(fields, settingField{Key: key, Label: label, Desc: desc, Value: value})
	}
	return settingsView{Fields: fields}
}

// effectiveSetting returns the in-effect value for env-backed settings so the
// form reflects reality rather than the hard-coded default.
func effectiveSetting(cfg *config.Config, key string) (string, bool) {
	switch key {
	case service.SettingBrowserSolverEnabled:
		return strconv.FormatBool(cfg.BrowserSolver.Enabled), true
	case service.SettingBrowserSolverProvider:
		return cfg.BrowserSolver.Provider, true
	case service.SettingBrowserSolverEndpoint:
		return cfg.BrowserSolver.Endpoint, true
	case service.SettingBrowserSolverTimeoutSeconds:
		return strconv.Itoa(cfg.BrowserSolver.TimeoutSeconds), true
	case service.SettingBrowserDownloaderEnabled:
		return strconv.FormatBool(cfg.BrowserDownload.Enabled), true
	case service.SettingBrowserDownloaderEndpoint:
		return cfg.BrowserDownload.Endpoint, true
	case service.SettingBrowserDownloaderTimeoutSeconds:
		return strconv.Itoa(cfg.BrowserDownload.TimeoutSeconds), true
	}
	return "", false
}

// jobState classifies a job status for the UI: active covers pending retries
// (a "failed" job is re-claimed on backoff); dead is the terminal failure.
func jobState(status string) (active, failed bool) {
	switch status {
	case "queued", "running", "failed":
		return true, false
	case "dead":
		return false, true
	}
	return false, false
}

// titleActivity reports which of a title's job types are running (for button
// locking), a verb for the spinner, and the most recent terminal failure.
func (u *webUI) titleActivity(ctx context.Context, id int64) activityView {
	title, err := u.svc.GetTitle(ctx, id)
	if err != nil {
		title = library.Title{ID: id, Monitored: true}
	}
	running, label, failed, msg := titleActivityFrom(u.jobs(ctx), title)
	return activityView{Title: title, Running: running, ActiveLabel: label, Failed: failed, Error: msg}
}

func titleActivityFrom(js []jobs.Job, title library.Title) (running map[string]bool, label string, failed bool, msg string) {
	running = map[string]bool{}
	for _, j := range js { // List is newest-first
		var p service.JobPayload
		if json.Unmarshal([]byte(j.Payload), &p) != nil {
			continue
		}
		if p.TitleID != title.ID {
			continue
		}
		verb := titleVerb(j.Type)
		if verb == "" {
			continue
		}
		active, isFailed := jobState(j.Status)
		switch {
		case active:
			running[j.Type] = true
			if label == "" {
				label = verb
			}
		case isFailed && !failed:
			failed, msg = true, j.LastError
		}
	}
	if len(running) > 0 {
		failed = false // something is running; don't also show the last failure
	}
	return running, label, failed, msg
}

func titleVerb(typ string) string {
	switch typ {
	case jobs.TypeRefreshTitle:
		return "refreshing"
	case jobs.TypeDownloadMissing:
		return "downloading"
	case jobs.TypeScanDownloads:
		return "scanning"
	}
	return ""
}

// jobStateFor reports the newest job of one type matching payload.
func (u *webUI) jobStateFor(ctx context.Context, typ string, payload service.JobPayload) (active, failed bool, msg string) {
	want, _ := json.Marshal(payload)
	for _, j := range u.jobs(ctx) {
		if j.Type == typ && j.Payload == string(want) {
			active, failed = jobState(j.Status)
			return active, failed, j.LastError
		}
	}
	return false, false, ""
}

func (u *webUI) jobs(ctx context.Context) []jobs.Job {
	all, _ := u.svc.List(ctx)
	return all
}

func anyActive(js []jobs.Job) bool {
	for _, j := range js {
		if j.Status == "queued" || j.Status == "running" {
			return true
		}
	}
	return false
}

func navFor(path string) string {
	switch {
	case path == "/":
		return "dashboard"
	case strings.HasPrefix(path, "/search"):
		return "search"
	case strings.HasPrefix(path, "/library"):
		return "library"
	case strings.HasPrefix(path, "/import"):
		return "import"
	case strings.HasPrefix(path, "/sources"):
		return "sources"
	case strings.HasPrefix(path, "/settings"):
		return "settings"
	}
	return ""
}

func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func (u *webUI) funcs() template.FuncMap {
	return template.FuncMap{
		"mangaTitle": mangaTitle,
		"since":      since,
		"confidence": func(c float64) string { return fmt.Sprintf("%.0f%%", c*100) },
		"orUnknown":  func(s string) string { return orDefault(s, "unknown") },
		"orDash":     func(s string) string { return orDefault(s, "—") },
		"linked":     func(s string) bool { return strings.HasPrefix(s, "http") },
		"imported":   func(s string) bool { return strings.HasPrefix(s, "local:") },
		"pathEscape": url.PathEscape,
		"humanBytes": util.Human,
		"releaseStatus": func(s string) string {
			return strings.ToLower(strings.ReplaceAll(s, "_", " "))
		},
		"pct":       func(done, total int64) int64 { return percent(done, total) },
		"sourceRow": func(s sources.Source) sourceRowView { return sourceRowView{Source: s} },
		"missingTotal": func(ts []library.Title) int64 {
			var n int64
			for _, t := range ts {
				n += t.MissingCount
			}
			return n
		},
		"healthyCount": func(ss []sources.Source) int {
			n := 0
			for _, s := range ss {
				if s.Status == sources.StatusHealthy {
					n++
				}
			}
			return n
		},
	}
}

// chapterSource names where a chapter came from, derived from its URL.
func chapterSource(rawURL string) string {
	if strings.HasPrefix(rawURL, "local:") {
		return "imported"
	}
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		return strings.TrimPrefix(u.Host, "www.")
	}
	return "—"
}

func mangaTitle(m catalog.Manga) string {
	for _, t := range []string{m.TitleEnglish, m.TitleRomaji, m.TitleNative} {
		if strings.TrimSpace(t) != "" {
			return t
		}
	}
	return "Untitled"
}

func jobLabel(typ string) string {
	switch typ {
	case jobs.TypeRefreshTitle:
		return "Refresh chapters"
	case jobs.TypeScanDownloads:
		return "Scan downloads"
	case jobs.TypeDownloadMissing:
		return "Download missing"
	case jobs.TypeVerifySource:
		return "Verify source"
	case jobs.TypeMatchSources:
		return "Match sources"
	}
	return typ
}

func since(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func orDefault(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func percent(done, total int64) int64 {
	if total <= 0 {
		return 0
	}
	if done > total {
		done = total
	}
	return done * 100 / total
}
