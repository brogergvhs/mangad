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
	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/service"
	"github.com/brogergvhs/mangad/internal/sources"
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
type dashData struct {
	Titles    []library.Title
	Sources   []sources.Source
	Jobs      []jobs.Job
	AnyActive bool
}
type activityView struct {
	Title         library.Title
	Manga         catalog.Manga
	ChaptersTable tableData
	Sources       matchView
	Running       map[string]bool // job type -> active (for button locking)
	ActiveLabel   string
	Failed        bool
	Error         string
}
type sourceRowView struct {
	Source sources.Source
	Active bool
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
	case service.SettingSourceRegistryURL:
		return "Extra source list URL", "Optional URL to load additional scraper definitions from. Leave blank to use built-in sources."
	case service.SettingJobsMaxAttempts:
		return "Job retry limit", "How many times a failed background job (refresh, scan) is retried before it is given up."
	case service.SettingJobsTimeout:
		return "Job time limit", "Maximum time a single background job may run before it is aborted (e.g. 10m)."
	case service.SettingDownloadsMaxAttempts:
		return "Download retry limit", "How many times a failed chapter download is retried before giving up."
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
	mux.HandleFunc("POST /ui/library/{id}/remove", u.libRemove)
	mux.HandleFunc("POST /ui/library/{id}/find-sources", u.findSources)
	mux.HandleFunc("GET /ui/library/{id}/sources", u.titleSources)
	mux.HandleFunc("GET /ui/library/{id}/chapters", u.chaptersTable)
	mux.HandleFunc("POST /ui/library/{id}/link", u.linkSource)
	mux.HandleFunc("POST /ui/library/{id}/link-url", u.linkURL)
	mux.HandleFunc("POST /ui/import/{folder}/search", u.importSearch)
	mux.HandleFunc("POST /ui/import", u.importDo)
	mux.HandleFunc("POST /ui/sources/{id}/verify", u.srcVerify)
	mux.HandleFunc("GET /ui/sources/{id}/row", u.srcRow)
	mux.HandleFunc("POST /ui/sources/sync", u.srcSync)
	mux.HandleFunc("POST /ui/sources/test", u.srcTest)
	mux.HandleFunc("POST /ui/sources/custom", u.srcAddCustom)
	mux.HandleFunc("GET /ui/library/table", u.libraryTable)
	mux.HandleFunc("GET /ui/jobs/table", u.jobsTable)
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

func (u *webUI) frag(w http.ResponseWriter, name string, data any) {
	if err := u.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (u *webUI) fail(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusBadRequest)
	u.frag(w, "toast", toastView{Msg: err.Error()})
}

// --- pages ---

func (u *webUI) dashboard(w http.ResponseWriter, r *http.Request) {
	titles, _ := u.svc.ListTitles(r.Context())
	srcs, _ := u.svc.ListSources(r.Context())
	js, _ := u.svc.List(r.Context())
	u.page(w, r, "dashboard", "Dashboard", dashData{Titles: titles, Sources: srcs, Jobs: js, AnyActive: anyActive(js)})
}

const (
	libraryPerPage = 20
	jobsPerPage    = 15
)

func (u *webUI) libraryPage(w http.ResponseWriter, r *http.Request) {
	u.page(w, r, "library", "Library", u.buildLibraryTable(r.Context(), r.URL.Query()))
}

func (u *webUI) libraryTable(w http.ResponseWriter, r *http.Request) {
	u.frag(w, "table", u.buildLibraryTable(r.Context(), r.URL.Query()))
}

func (u *webUI) buildLibraryTable(ctx context.Context, values url.Values) tableData {
	page, key, dir := tableParams(values, libraryPerPage)
	titles, _ := u.svc.ListTitles(ctx)
	sortTitles(titles, key, dir)
	pageTitles, total := paginate(titles, page, libraryPerPage)
	js := u.jobs(ctx)

	t := tableData{
		ID: "library-table", BaseURL: "/ui/library/table",
		Page: page, PerPage: libraryPerPage, Total: total, Sort: key, Dir: dir,
		Empty: "Nothing in your library yet — add manga from Search or Import a collection.",
		Columns: []tableColumn{
			{Label: ""},
			{Label: "Title", SortKey: "title"},
			{Label: "Chapters", SortKey: "missing"},
			{Label: "Monitor"},
			{Label: "Status"},
		},
	}
	for _, tl := range pageTitles {
		running, label, failed, msg := titleActivityFrom(js, tl.ID)
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
		},
	}
	for _, j := range rows {
		t.Rows = append(t.Rows, tableRow{
			ID: strconv.FormatInt(j.ID, 10),
			Cells: []template.HTML{
				text(jobLabel(j.Type)),
				u.renderToHTML("jobStatusBadge", j),
				text(strconv.Itoa(j.Attempts)),
				text(since(j.UpdatedAt)),
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
	view.ChaptersTable = u.buildChaptersTable(r.Context(), title, r.URL.Query())
	view.Sources = u.sourceView(r.Context(), title)
	if title.CatalogMangaID != nil {
		view.Manga, _ = u.svc.GetManga(r.Context(), *title.CatalogMangaID)
	}
	u.page(w, r, "title", title.DisplayTitle, view)
}

const chaptersPerPage = 25

func (u *webUI) chaptersTable(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	title, err := u.svc.GetTitle(r.Context(), id)
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
			{Label: "Source"},
		},
	}
	for _, c := range rows {
		t.Rows = append(t.Rows, tableRow{
			ID: strconv.FormatInt(c.ID, 10),
			Cells: []template.HTML{
				u.renderToHTML("chapterName", c),
				u.renderToHTML("chapterStatus", c),
				text(chapterSource(c.URL)),
			},
		})
	}
	return t
}

// sourceView builds the linkable-source list for a title's detail page.
func (u *webUI) sourceView(ctx context.Context, title library.Title) matchView {
	if title.CatalogMangaID == nil {
		return titleSourceView(title, false, false, "", nil)
	}
	cid := *title.CatalogMangaID
	active, failed, msg := u.jobStateFor(ctx, jobs.TypeMatchSources, service.JobPayload{CatalogID: cid})
	if active {
		return titleSourceView(title, true, false, "", nil)
	}
	matches, _ := u.svc.ListMatches(ctx, cid)
	return titleSourceView(title, false, failed, msg, matches)
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

func (u *webUI) search(w http.ResponseWriter, r *http.Request) {
	items, err := u.svc.SearchAniList(r.Context(), r.FormValue("q"), 10)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "searchResults", items)
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
	u.frag(w, "matches", u.sourceView(r.Context(), title))
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
	w.Header().Set("HX-Redirect", fmt.Sprintf("/library/%d", id))
}

func (u *webUI) linkURL(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if _, err := u.svc.LinkTitleURL(r.Context(), id, r.FormValue("url")); err != nil {
		u.fail(w, err)
		return
	}
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
	profile, _, _, err := customProfileFromForm(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := u.svc.ImportLocalSource(r.Context(), profile); err != nil {
		u.fail(w, err)
		return
	}
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
	name = firstNonEmpty(name, host)
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
		RequiresBrowserSolver: solver, RequiresBrowserDownload: browser, Enabled: true,
	}, solver, browser, nil
}

func formChecked(r *http.Request, name string) bool {
	switch r.FormValue(name) {
	case "on", "true", "1":
		return true
	}
	return false
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
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
	keys := service.SettingKeys()
	fields := make([]settingField, 0, len(keys))
	for _, key := range keys {
		label, desc := settingMeta(key)
		fields = append(fields, settingField{
			Key:   key,
			Label: label,
			Desc:  desc,
			Value: u.svc.Setting(ctx, key, service.SettingDefault(key)),
		})
	}
	return settingsView{Fields: fields}
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
	running, label, failed, msg := titleActivityFrom(u.jobs(ctx), id)
	return activityView{Running: running, ActiveLabel: label, Failed: failed, Error: msg}
}

func titleActivityFrom(js []jobs.Job, id int64) (running map[string]bool, label string, failed bool, msg string) {
	running = map[string]bool{}
	for _, j := range js { // List is newest-first
		var p service.JobPayload
		if json.Unmarshal([]byte(j.Payload), &p) != nil || p.TitleID != id {
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
		"mangaTitle":    mangaTitle,
		"jobLabel":      jobLabel,
		"since":         since,
		"confidence":    func(c float64) string { return fmt.Sprintf("%.0f%%", c*100) },
		"orUnknown":     func(s string) string { return orDefault(s, "unknown") },
		"orDash":        func(s string) string { return orDefault(s, "—") },
		"linked":        func(s string) bool { return strings.HasPrefix(s, "http") },
		"imported":      func(s string) bool { return strings.HasPrefix(s, "local:") },
		"chapterSource": chapterSource,
		"pathEscape":    url.PathEscape,
		"pct":           func(done, total int64) int64 { return percent(done, total) },
		"sourceRow":     func(s sources.Source) sourceRowView { return sourceRowView{Source: s} },
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
