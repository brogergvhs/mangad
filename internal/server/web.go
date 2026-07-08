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
	Title       library.Title
	Chapters    []library.ChapterStatus
	Sources     matchView
	Running     map[string]bool // job type -> active (for button locking)
	ActiveLabel string
	Failed      bool
	Error       string
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
	Keys   []string
	Values map[string]string
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
	mux.HandleFunc("POST /ui/library/{id}/link", u.linkSource)
	mux.HandleFunc("POST /ui/import/{folder}/search", u.importSearch)
	mux.HandleFunc("POST /ui/import", u.importDo)
	mux.HandleFunc("POST /ui/sources/{id}/verify", u.srcVerify)
	mux.HandleFunc("GET /ui/sources/{id}/row", u.srcRow)
	mux.HandleFunc("POST /ui/sources/sync", u.srcSync)
	mux.HandleFunc("GET /ui/jobs", u.jobsFragment)
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

func (u *webUI) libraryPage(w http.ResponseWriter, r *http.Request) {
	titles, err := u.svc.ListTitles(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	js := u.jobs(r.Context())
	views := make([]activityView, 0, len(titles))
	for _, t := range titles {
		running, label, failed, msg := titleActivityFrom(js, t.ID)
		views = append(views, activityView{Title: t, Running: running, ActiveLabel: label, Failed: failed, Error: msg})
	}
	u.page(w, r, "library", "Library", views)
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
	chapters, _ := u.svc.TitleChapters(r.Context(), id)
	view := u.titleActivity(r.Context(), id)
	view.Title = title
	view.Chapters = chapters
	view.Sources = u.sourceView(r.Context(), title)
	u.page(w, r, "title", title.DisplayTitle, view)
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

// --- jobs & settings ---

func (u *webUI) jobsFragment(w http.ResponseWriter, r *http.Request) {
	js, _ := u.svc.List(r.Context())
	u.frag(w, "jobsList", dashData{Jobs: js, AnyActive: anyActive(js)})
}

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
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		values[key] = u.svc.Setting(ctx, key, service.SettingDefault(key))
	}
	return settingsView{Keys: keys, Values: values}
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
