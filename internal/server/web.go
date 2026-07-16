package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brogergvhs/mangad/internal/auth"
	"github.com/brogergvhs/mangad/internal/catalog"
	"github.com/brogergvhs/mangad/internal/config"
	"github.com/brogergvhs/mangad/internal/jobs"
	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/providers/registry"
	"github.com/brogergvhs/mangad/internal/service"
	"github.com/brogergvhs/mangad/internal/sources"
	"github.com/brogergvhs/mangad/internal/util"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type webUI struct {
	svc      *service.JobService
	tmpl     *template.Template
	kick     func() // start the job runner now, non-blocking
	assetVer string
}

type pageData struct {
	Title, Nav string
	User       *auth.User
	Theme      string
	ThemeCSS   template.CSS // custom-theme variable overrides
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
	TotalBytes int64
	TotalPages int64
	TotalChaps int64
	User       *auth.User
}
type libraryView struct {
	Controls libraryControls
	Table    libraryResults
}
type libraryControls struct {
	Q        string
	Monitor  string
	Fav      string
	Source   string
	Progress string
	Sort     string
	Dir      string
	View     string
}
type healthView struct {
	Services []service.ServiceHealth
	Interval string // HTMX poll interval, e.g. "60s"
}
type activityView struct {
	Title         library.Title
	Manga         catalog.Manga
	Sources       matchView
	LinkedSources []linkedSourceView // sources already linked to this title
	SingleSources []sources.Source   // single-manga sources selectable for linking
	LinkSources   []sources.Source   // searchable sources for specifying a page URL
	RefreshEvery  string             // effective global refresh cadence
	Running       map[string]bool    // job type -> active (for button locking)
	ActiveLabel   string
	Queued        []string
	Failed        bool
	Error         string
	User          *auth.User
	AniList       service.AniListConnection
	Content       titleContentView
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
	Groups        []settingGroup
	AniList       service.AniListConnection
	RedirectURL   string
	AppConfigured bool
}
type settingGroup struct {
	Title  string
	Fields []settingField
}
type settingField struct {
	Key, Label, Desc, Value string
	Kind                    string // "", "select", "color"
	Options                 []string
}

// settingMeta maps a technical setting key to a human label and description.
func settingMeta(key string) (label, desc string) {
	if token, ok := strings.CutPrefix(key, "ui.custom."); ok {
		return token, ""
	}
	switch key {
	case service.SettingServeAniListSyncEvery:
		return "AniList sync", "How often connected accounts sync reading progress with AniList (e.g. 12h). Empty disables."
	case service.SettingServeCatalogEvery:
		return "Catalog refresh", "How often cached AniList metadata (tags, release status) is re-fetched for tracked titles. Empty disables."
	case service.SettingRateLimitIntervalMS:
		return "Request pacing (ms)", "Minimum milliseconds between page requests to the same site. Image downloads are exempt. Lower is faster; too low risks bans."
	case service.SettingRateLimitBurst:
		return "Request pacing burst", "How many page requests may go out back-to-back before pacing kicks in."
	case service.SettingRateLimitDisabled:
		return "Disable request pacing", "true turns off per-site pacing entirely (not recommended for Cloudflare-fronted sites)."
	case service.SettingAniListClientID:
		return "AniList client ID", "From your AniList developer application."
	case service.SettingAniListClientSecret:
		return "AniList client secret", "Kept server-side; used to exchange login codes for tokens."
	case service.SettingUITheme:
		return "Theme", "Interface color theme. Pick custom to use the colors below."
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

// staticAssetVersion hashes the embedded static files so asset URLs change
// with every build that touches them.
func staticAssetVersion() string {
	h := fnv.New64a()
	_ = fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := staticFS.ReadFile(path)
		if err == nil {
			_, _ = h.Write([]byte(path))
			_, _ = h.Write(data)
		}
		return nil
	})
	return strconv.FormatUint(h.Sum64(), 36)
}

// registerUI mounts the server-rendered UI and its HTMX endpoints on mux.
func registerUI(mux *http.ServeMux, svc *service.JobService, runJobs func(context.Context) (service.RunSummary, error)) {
	u := &webUI{svc: svc, kick: func() {
		if runJobs != nil {
			go func() { _, _ = runJobs(context.Background()) }()
		}
	}}
	u.assetVer = staticAssetVersion()
	u.tmpl = template.Must(template.New("").Funcs(u.funcs()).ParseFS(templateFS, "templates/*.html"))

	static, _ := fs.Sub(staticFS, "static")
	// Assets are addressed with a content-hash query (?v=...), so they can be
	// cached hard; a new build changes the URL and busts stale copies (embedded
	// files carry no modtime, so browsers would otherwise cache heuristically).
	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(static)))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		staticHandler.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /{$}", u.homePage)
	mux.HandleFunc("GET /management", u.management)
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
	mux.HandleFunc("POST /ui/library/{id}/favourite", u.libFavourite)
	mux.HandleFunc("POST /ui/library/{id}/refresh-interval", u.libRefreshInterval)
	mux.HandleFunc("POST /ui/library/{id}/remove", u.libRemove)
	mux.HandleFunc("POST /ui/library/{id}/find-sources", u.findSources)
	mux.HandleFunc("GET /ui/library/{id}/sources", u.titleSources)
	mux.HandleFunc("GET /ui/library/{id}/chapters", u.chaptersTable)
	mux.HandleFunc("GET /ui/library/{id}/content", u.titleContentFrag)
	mux.HandleFunc("POST /ui/library/{id}/volumes/range", u.volumesRange)
	mux.HandleFunc("POST /ui/volumes/{id}/read", u.volumeRead(true))
	mux.HandleFunc("POST /ui/volumes/{id}/unread", u.volumeRead(false))
	mux.HandleFunc("GET /ui/volumes/{id}/cover", u.volumeCover)
	mux.HandleFunc("POST /ui/volumes/{id}/cover", u.volumeCoverUpload)
	mux.HandleFunc("POST /ui/volumes/{id}/cover/reset", u.volumeCoverReset)
	mux.HandleFunc("POST /ui/import/attach-volumes", u.importAttachVolumes)
	mux.HandleFunc("GET /ui/library/{id}/progress", u.titleProgress)
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
	mux.HandleFunc("POST /ui/sources/{id}/enabled", u.srcEnabled)
	mux.HandleFunc("POST /ui/sources/{id}/delete", u.srcDelete)
	mux.HandleFunc("POST /ui/sources/sync", u.srcSync)
	mux.HandleFunc("POST /ui/sources/test", u.srcTest)
	mux.HandleFunc("POST /ui/sources/custom", u.srcAddCustom)
	mux.HandleFunc("GET /ui/library/table", u.libraryTable)
	mux.HandleFunc("GET /ui/jobs/table", u.jobsTable)
	mux.HandleFunc("POST /ui/jobs/{id}/cancel", u.jobCancel)
	mux.HandleFunc("GET /ui/sessions", u.sessionsFrag)
	mux.HandleFunc("GET /ui/health", u.health)
	mux.HandleFunc("GET /ui/import/candidates", u.importCandidates)
	mux.HandleFunc("GET /ui/library/{id}/related", u.relatedManga)
	mux.HandleFunc("GET /ui/search/trending", u.trendingManga)
	mux.HandleFunc("GET /anilist/connect", u.anilistConnect)
	mux.HandleFunc("GET /anilist/callback", u.anilistCallback)
	mux.HandleFunc("POST /ui/anilist/disconnect", u.anilistDisconnect)
	mux.HandleFunc("GET /ui/anilist/library", u.anilistLibrary)
	mux.HandleFunc("POST /ui/anilist/sync", u.anilistSyncNow)
	mux.HandleFunc("GET /ui/anilist/suggestions", u.anilistSuggestions)
	mux.HandleFunc("POST /ui/library/{id}/anilist-sync", u.anilistSyncTitle)
	mux.HandleFunc("GET /ui/account", u.accountFrag)
	mux.HandleFunc("POST /ui/account/password", u.accountPassword)
	mux.HandleFunc("POST /ui/account/sessions/revoke", u.accountRevokeSessions)
	mux.HandleFunc("POST /ui/account/tokens", u.accountTokenCreate)
	mux.HandleFunc("POST /ui/account/tokens/{id}/delete", u.accountTokenDelete)
	mux.HandleFunc("GET /users", u.usersPage)
	mux.HandleFunc("GET /ui/users", u.usersFrag)
	mux.HandleFunc("POST /ui/users", u.userCreate)
	mux.HandleFunc("GET /ui/users/{id}/edit", u.userEditModal)
	mux.HandleFunc("GET /ui/users/roles/{id}/edit", u.roleEditModal)
	mux.HandleFunc("POST /ui/users/{id}", u.userUpdate)
	mux.HandleFunc("POST /ui/users/{id}/delete", u.userDelete)
	mux.HandleFunc("POST /ui/users/roles", u.roleSave)
	mux.HandleFunc("POST /ui/users/roles/{id}/delete", u.roleDelete)
	mux.HandleFunc("PUT /ui/settings", u.settingsSave)
}

// --- rendering ---

func (u *webUI) page(w http.ResponseWriter, r *http.Request, content, title string, data any) {
	var buf bytes.Buffer
	if err := u.tmpl.ExecuteTemplate(&buf, content, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	theme, css := u.theme(r.Context())
	if err := u.tmpl.ExecuteTemplate(w, "layout.html", pageData{Title: title, Nav: navFor(r.URL.Path), User: userFrom(r.Context()), Theme: theme, ThemeCSS: css, Content: template.HTML(buf.String())}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type mangaResults struct {
	Heading string
	View    string // "cards" (default) or "table"
	Items   []searchResultView
	CanAdd  bool
}

// resultView picks the search results layout from the request.
func resultView(r *http.Request) string {
	switch v := r.FormValue("view"); v {
	case "table", "full":
		return v
	default:
		return "cards"
	}
}

func (u *webUI) mangaResultsView(ctx context.Context, heading, view string, items []catalog.Manga) mangaResults {
	user := userFrom(ctx)
	return mangaResults{Heading: heading, View: view, Items: u.stripItems(ctx, items), CanAdd: user.Can(auth.PermLibraryAdd)}
}

func (u *webUI) stripItems(ctx context.Context, items []catalog.Manga) []searchResultView {
	inLibrary, _ := u.svc.TitlesByProvider(ctx, catalog.AniListProvider)
	views := make([]searchResultView, 0, len(items))
	for _, m := range items {
		if !contentAllowed(ctx, m.IsAdult, mangaContentTags(m)) {
			continue
		}
		views = append(views, searchResultView{Manga: m, TitleID: inLibrary[m.ProviderID]})
	}
	return views
}

func (u *webUI) relatedManga(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	title, err := u.svc.GetTitle(r.Context(), id)
	if err != nil || title.CatalogMangaID == nil {
		u.frag(w, "mangaResults", mangaResults{})
		return
	}
	items, err := u.svc.RelatedManga(r.Context(), *title.CatalogMangaID, 12)
	if err != nil {
		u.frag(w, "mangaResults", mangaResults{})
		return
	}
	u.frag(w, "mangaResults", u.mangaResultsView(r.Context(), "", "cards", items))
}

func (u *webUI) trendingManga(w http.ResponseWriter, r *http.Request) {
	items, err := u.svc.RecommendedManga(r.Context(), 18)
	if err != nil {
		u.frag(w, "mangaResults", mangaResults{})
		return
	}
	u.frag(w, "mangaResults", u.mangaResultsView(r.Context(), "", resultView(r), items))
}

// contentAllowed reports whether the acting user may see content with the
// given adult flag and tag/genre set. The env admin is never restricted.
func contentAllowed(ctx context.Context, isAdult bool, tags []string) bool {
	u := auth.FromContext(ctx)
	if u == nil {
		return false
	}
	if isAdult && !u.AllowAdult {
		return false
	}
	for _, blocked := range u.BlockedTags {
		for _, tag := range tags {
			if strings.EqualFold(strings.TrimSpace(blocked), strings.TrimSpace(tag)) {
				return false
			}
		}
	}
	return true
}

// mangaContentTags collects the tag/genre vocabulary a catalog entry carries.
func mangaContentTags(m catalog.Manga) []string {
	return append(append([]string{}, m.Tags...), m.Genres...)
}

// filterRestrictedTitles hides titles the acting user must not see (adult
// flag or blocked tags/genres).
func filterRestrictedTitles(ctx context.Context, titles []library.Title) []library.Title {
	out := titles[:0]
	for _, t := range titles {
		if contentAllowed(ctx, t.IsAdult, t.ContentTags) {
			out = append(out, t)
		}
	}
	return out
}

// theme returns the active UI theme and, for the custom theme, a CSS block of
// its color variables (values are hex-validated on save, so safe to embed).
func (u *webUI) theme(ctx context.Context) (string, template.CSS) {
	stored := u.svc.AllSettings(ctx) // global fallback (pre-multi-user values)
	for k, v := range u.svc.UserSettings(ctx, auth.UserID(ctx)) {
		stored[k] = v // personal appearance wins
	}
	theme := stored[service.SettingUITheme]
	if theme == "" {
		theme = service.SettingDefault(service.SettingUITheme)
	}
	if theme != "custom" {
		return theme, ""
	}
	var b strings.Builder
	b.WriteString(`[data-theme="custom"]{`)
	for _, token := range service.CustomColorTokens() {
		key := service.CustomColorKey(token)
		value, ok := stored[key]
		if !ok {
			value = service.SettingDefault(key)
		}
		if service.ValidateSetting(key, value) == nil {
			fmt.Fprintf(&b, "--color-%s:%s;", token, value)
		}
	}
	b.WriteString("}")
	return theme, template.CSS(b.String())
}

func (u *webUI) readerLayout(w http.ResponseWriter, r *http.Request, title string, data readerView) {
	theme, css := u.theme(r.Context())
	if err := u.tmpl.ExecuteTemplate(w, "reader_layout.html", pageData{Title: title, Theme: theme, ThemeCSS: css, Content: u.renderToHTML("reader", data)}); err != nil {
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

func (u *webUI) management(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !user.Can(auth.PermStatsView) && !user.Can(auth.PermServicesView) && !user.Can(auth.PermSessionsView) && !user.Can(auth.PermJobsView) {
		writeError(w, http.StatusForbidden, "missing a management permission")
		return
	}
	titles, _ := u.svc.ListTitles(r.Context())
	titles = filterRestrictedTitles(r.Context(), titles)
	srcs, _ := u.svc.ListSources(r.Context())
	data := dashData{Titles: titles, Sources: srcs, User: user} // services health post-loads via /ui/health
	for _, t := range titles {
		data.TotalBytes += t.SizeBytes + t.VolumeBytes
		data.TotalPages += t.Pages + t.VolumePages
		data.TotalChaps += t.DiscoveredCount
	}
	u.page(w, r, "dashboard", "Management", data)
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
	u.frag(w, "libraryResults", u.buildLibraryTable(r.Context(), r.URL.Query()))
}

// libraryResults renders the library as a table, a card grid, or both
// (responsive auto mode).
type libraryResults struct {
	tableData
	Cards     []library.Title
	Fulls     []libraryFullRow // detailed rows for the full view
	CanManage bool
	View      string
}

type libraryFullRow struct {
	Title library.Title
	Manga catalog.Manga
}

func (u *webUI) readerPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	volumesMode := r.URL.Query().Get("mode") == "volumes"
	var progress library.TitleReadProgress
	var err2 error
	if volumesMode {
		progress, err2 = u.svc.VolumesReaderProgress(r.Context(), id)
	} else {
		progress, err2 = u.svc.ReaderProgress(r.Context(), id)
	}
	if err2 != nil {
		http.Error(w, err2.Error(), http.StatusBadRequest)
		return
	}
	if !contentAllowed(r.Context(), progress.Title.IsAdult, progress.Title.ContentTags) {
		http.Error(w, "adult content is not available for this account", http.StatusForbidden)
		return
	}
	presence.SetTitle(r.Context(), auth.UserID(r.Context()), progress.ID, progress.DisplayTitle)
	currentID, _ := strconv.ParseInt(r.URL.Query().Get("chapter"), 10, 64)
	manifest, prevID, nextID := readerManifestWindowMode(progress, currentID, volumesMode)
	data := readerView{Title: progress.Title, Manifest: manifest, PagePosition: initialReaderPosition(manifest)}
	if len(data.Manifest.Chapters) == 0 {
		data.Empty = "No downloaded chapters are available to read yet."
	}
	mode := ""
	if volumesMode {
		mode = "&mode=volumes"
	}
	if prevID > 0 {
		data.PrevURL = fmt.Sprintf("/reader/%d?chapter=%d%s", progress.ID, prevID, mode)
	}
	if nextID > 0 {
		data.NextURL = fmt.Sprintf("/reader/%d?chapter=%d%s", progress.ID, nextID, mode)
	}
	if raw, err := json.Marshal(data.Manifest); err == nil {
		data.ManifestJSON = template.JS(raw)
	}
	u.readerLayout(w, r, progress.DisplayTitle, data)
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

func (u *webUI) buildLibraryTable(ctx context.Context, values url.Values) libraryResults {
	page, _, _ := tableParams(values, libraryPerPage)
	controls := libraryControlsFrom(values)
	titles, _ := u.svc.ListTitles(ctx)
	titles = filterRestrictedTitles(ctx, titles)
	allCount := len(titles)
	titles = filterTitles(titles, controls)
	sortTitles(titles, controls.Sort, controls.Dir)
	pageTitles, total := paginate(titles, page, libraryPerPage)
	js := u.jobs(ctx)
	empty := "Nothing in your library yet — add manga from Search or Import a collection."
	if allCount > 0 {
		empty = "No manga match the current search or filters."
	}

	t := libraryResults{View: controls.View, CanManage: auth.FromContext(ctx).Can(auth.PermLibraryManage)}
	t.tableData = tableData{
		ID: "library-table", BaseURL: "/ui/library/table",
		Page: page, PerPage: libraryPerPage, Total: total, Sort: controls.Sort, Dir: controls.Dir,
		Params: libraryTableParams(values),
		Empty:  empty,
		Columns: []tableColumn{
			{Label: ""},
			{Label: "Title"},
			{Label: "Chapters"},
		},
	}
	t.Cards = pageTitles
	catalogIDs := make([]int64, 0, len(pageTitles))
	for _, tl := range pageTitles {
		if tl.CatalogMangaID != nil {
			catalogIDs = append(catalogIDs, *tl.CatalogMangaID)
		}
	}
	mangas, _ := u.svc.MangaByIDs(ctx, catalogIDs) // one query for all page rows
	for _, tl := range pageTitles {
		running, label, queued, failed, msg := titleActivityFrom(js, tl)
		_ = queued
		if len(running) > 0 {
			t.Poll = true
		}
		view := activityView{Title: tl, Running: running, ActiveLabel: label, Failed: failed, Error: msg, User: auth.FromContext(ctx)}
		full := libraryFullRow{Title: tl}
		var detail template.HTML
		if tl.CatalogMangaID != nil {
			if m, ok := mangas[*tl.CatalogMangaID]; ok {
				detail = u.renderToHTML("mangaDetail", m)
				full.Manga = m
			}
		}
		t.Fulls = append(t.Fulls, full)
		t.Rows = append(t.Rows, tableRow{
			ID: strconv.FormatInt(tl.ID, 10),
			Cells: []template.HTML{
				u.renderToHTML("cellCover", tl),
				u.renderToHTML("cellTitle", view),
				u.renderToHTML("progressBar", tl),
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
		Fav:      values.Get("fav"),
		Source:   values.Get("source"),
		Progress: values.Get("progress"),
		Sort:     values.Get("sort"),
		Dir:      values.Get("dir"),
		View:     values.Get("view"),
	}
	if c.View != "table" && c.View != "cards" && c.View != "full" {
		c.View = "auto"
	}
	if c.Monitor == "" {
		c.Monitor = "all"
	}
	if c.Fav != "only" {
		c.Fav = "all"
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
	for _, key := range []string{"q", "monitor", "source", "progress", "sort", "dir", "view"} {
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
		if c.Fav == "only" && !title.Favourite {
			continue
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

	// Group sweep-spawned jobs under the global job that created them. A big
	// sweep can push its own parent row outside the list window — fetch any
	// missing parents so their children never render ungrouped.
	children := map[int64][]jobs.Job{}
	seen := map[int64]bool{}
	for _, j := range all {
		seen[j.ID] = true
	}
	for _, j := range all {
		if j.ParentID != 0 && !seen[j.ParentID] {
			if parent, err := u.svc.GetJob(r.Context(), j.ParentID); err == nil {
				all = append(all, parent)
				seen[parent.ID] = true
			} else {
				seen[j.ParentID] = false // truly gone: child stays top-level
			}
		}
	}
	var top []jobs.Job
	for _, j := range all {
		if j.ParentID != 0 && seen[j.ParentID] {
			children[j.ParentID] = append(children[j.ParentID], j)
		} else {
			top = append(top, j)
		}
	}
	sortJobs(top, key, dir)
	rows, total := paginate(top, page, jobsPerPage)

	t := tableData{
		ID: "jobs-table", BaseURL: "/ui/jobs/table",
		Page: page, PerPage: jobsPerPage, Total: total, Sort: key, Dir: dir,
		Poll: anyActive(all), Empty: "No jobs yet.",
		Columns: []tableColumn{
			{Label: ""},
			{Label: ""},
		},
	}
	for _, j := range rows {
		kids := children[j.ID]
		activeKids, deadKids, cancelledKids := 0, 0, 0
		for _, k := range kids {
			if a, _ := jobState(k.Status); a {
				activeKids++
			}
			switch k.Status {
			case "dead":
				deadKids++
			case "cancelled":
				cancelledKids++
			}
		}
		selfActive, _ := jobState(j.Status)
		// A group is only "done" once every child finished successfully; the
		// status icon rolls up the children's worst outcome.
		display := j
		if len(kids) > 0 {
			switch {
			case selfActive || activeKids > 0:
				display.Status = "running"
			case deadKids > 0:
				display.Status = "dead"
			case cancelledKids > 0:
				display.Status = "cancelled"
			}
		}
		detail := u.renderToHTML("jobDetail", j)
		if len(kids) > 0 {
			detail = u.renderToHTML("jobChildren", kids)
		}
		t.Rows = append(t.Rows, tableRow{
			ID: strconv.FormatInt(j.ID, 10),
			Cells: []template.HTML{
				u.renderToHTML("jobCell", jobGroupView{Job: j, Children: len(kids), ActiveChildren: activeKids}),
				u.renderToHTML("jobActions", jobActionsView{Job: display, CanCancel: selfActive || activeKids > 0}),
			},
			Detail: detail,
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
	if !contentAllowed(r.Context(), title.IsAdult, title.ContentTags) {
		http.Error(w, "adult content is not available for this account", http.StatusForbidden)
		return
	}
	view := u.titleActivity(r.Context(), id)
	view.Title = title
	view.User = userFrom(r.Context())
	if title.CatalogMangaID != nil {
		view.AniList = u.svc.AniListConnectionFor(r.Context(), auth.UserID(r.Context()))
	}
	view.Content = u.buildTitleContent(r, title, r.URL.Query().Get("tab"))
	view.RefreshEvery = u.svc.Setting(r.Context(), service.SettingServeRefreshEvery, service.SettingDefault(service.SettingServeRefreshEvery))
	linked := u.linkedSourceIDs(r.Context(), id)
	allSources, _ := u.svc.ListSources(r.Context()) // one fetch for every source-derived section
	view.LinkedSources = u.linkedSourceViews(r.Context(), title)
	view.Sources = u.sourceView(r.Context(), title, linked, allSources)
	view.SingleSources = filterSources(singleMangaSources(allSources), linked)
	view.LinkSources = filterSources(searchableSources(allSources), linked)
	if title.CatalogMangaID != nil {
		view.Manga, _ = u.svc.GetManga(r.Context(), *title.CatalogMangaID)
	}
	u.page(w, r, "title", title.DisplayTitle, view)
}

// singleMangaSources returns enabled sources flagged as single-manga.
func singleMangaSources(all []sources.Source) []sources.Source {
	var out []sources.Source
	for _, s := range all {
		if s.SingleManga && s.Enabled {
			out = append(out, s)
		}
	}
	return out
}

// searchableSources returns enabled multi-manga sources, for specifying a page.
func searchableSources(all []sources.Source) []sources.Source {
	var out []sources.Source
	for _, s := range all {
		if s.Enabled && !s.SingleManga {
			out = append(out, s)
		}
	}
	return out
}

const chaptersPerPage = 25

// titleContentView drives the tabbed Chapters/Volumes section.
type titleContentView struct {
	Title           library.Title
	Tab             string // "chapters" or "volumes"
	ReadLabel       string
	VolumeReadLabel string
	ChaptersTable   chaptersView
	ChapterCount    int64
	Volumes         []volumeRowView
	VolumeCount     int
	Attaching       bool
}

func (u *webUI) buildTitleContent(r *http.Request, title library.Title, tab string) titleContentView {
	ctx := r.Context()
	vols, _ := u.svc.Volumes(ctx, title.ID)
	running, _, queued, _, _ := titleActivityFrom(u.jobs(ctx), title)
	attaching := running[jobs.TypeAttachVolumes] || slices.Contains(queued, "attaching volumes")
	if tab != "volumes" || (len(vols) == 0 && !attaching) {
		tab = "chapters"
	}
	view := titleContentView{
		Title:        title,
		Tab:          tab,
		ChapterCount: title.DiscoveredCount,
		Volumes:      volumeRows(vols),
		VolumeCount:  len(vols),
		Attaching:    attaching,
	}
	view.ReadLabel = "Read"
	if progress, err := u.svc.ReaderProgress(ctx, title.ID); err == nil && progress.NextChapterID != 0 && progress.ReadPages > 0 {
		view.ReadLabel = "Continue reading"
	}
	view.VolumeReadLabel = "Read"
	for _, v := range vols {
		if v.ReadPages > 0 || v.Read {
			view.VolumeReadLabel = "Continue reading"
			break
		}
	}
	if tab == "chapters" {
		view.ChaptersTable = u.buildChaptersTable(ctx, title, r.URL.Query())
		view.ChapterCount = int64(view.ChaptersTable.Total)
	}
	return view
}

func (u *webUI) titleContentFrag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.writeTitleContent(w, r, id, r.URL.Query().Get("tab"))
}

func (u *webUI) writeTitleContent(w http.ResponseWriter, r *http.Request, titleID int64, tab string) {
	title, err := u.svc.GetTitle(r.Context(), titleID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u.frag(w, "titleContent", u.buildTitleContent(r, title, tab))
}

// titleProgress re-renders the header progress bar; it polls while a job for
// the title is active so downloads update the page live.
type favView struct {
	ID        int64
	Favourite bool
}

func (u *webUI) libFavourite(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	fav, err := u.svc.ToggleFavourite(r.Context(), id)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "favToggle", favView{ID: id, Favourite: fav})
}

func (u *webUI) titleProgress(w http.ResponseWriter, r *http.Request) {
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
	running, _, queued, _, _ := titleActivityFrom(u.jobs(r.Context()), title)
	u.frag(w, "titleProgress", activityView{Title: title, Running: running, Queued: queued})
}

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
			if err == nil {
				u.svc.PushAniListEntry(r.Context(), auth.UserID(r.Context()), titleID)
			}
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
		if err == nil {
			u.svc.PushAniListEntry(r.Context(), auth.UserID(r.Context()), titleID)
		}
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
	u.frag(w, "chaptersList", u.buildChaptersTable(r.Context(), title, r.URL.Query()))
}

// jobGroupView labels a global job with its spawned-children counts.
type jobGroupView struct {
	jobs.Job
	Children       int
	ActiveChildren int
}

// jobActionsView pairs a job with whether its cancel action is available.
type jobActionsView struct {
	Job       jobs.Job
	CanCancel bool
}

// chaptersView drives the custom chapters list (reuses tableData paging).
type chaptersView struct {
	tableData
	Chapters []chapterRowView
	Poll     bool // a job for this title is active: keep the list fresh
}

type chapterRowView struct {
	library.ChapterStatus
	Percent int
	ReadTip string // e.g. "7/12 pages read"
	Size    string
	When    string
	Tint    string
}

func (u *webUI) buildChaptersTable(ctx context.Context, title library.Title, values url.Values) chaptersView {
	page, key, dir := tableParams(values, chaptersPerPage)
	// The sort dropdown submits one combined "order" value, e.g. "read-desc".
	if o := values.Get("order"); o != "" {
		if k, d, ok := strings.Cut(o, "-"); ok {
			key, dir = k, d
		}
	}
	if key == "" {
		key = "number"
	}
	chs, _ := u.svc.TitleChapters(ctx, title.ID)
	chs = filterChapters(chs, values.Get("q"))
	sortChapters(chs, key, dir)
	rows, total := paginate(chs, page, chaptersPerPage)

	empty := "Link a source to discover chapters."
	if strings.HasPrefix(title.SourceURL, "http") {
		empty = "No chapters yet — refresh to discover them."
	}
	t := chaptersView{tableData: tableData{
		ID: "chapters-table", BaseURL: fmt.Sprintf("/ui/library/%d/chapters", title.ID),
		Page: page, PerPage: chaptersPerPage, Total: total, Sort: key, Dir: dir, Empty: empty,
		Params: chapterTableParams(values),
	}}
	running, _, queued, _, _ := titleActivityFrom(u.jobs(ctx), title)
	t.Poll = len(running) > 0 || len(queued) > 0
	for _, c := range rows {
		size, when := "—", ""
		if c.Downloaded {
			size = util.Human(c.Bytes)
			if c.DownloadedAt != nil {
				when = c.DownloadedAt.Local().Format("02 Jan 2006")
			}
		}
		t.Chapters = append(t.Chapters, chapterRowView{
			ChapterStatus: c,
			Percent:       chapterReadPercent(c),
			ReadTip:       chapterReadTip(c),
			Size:          size,
			When:          when,
			Tint:          chapterRowClass(c),
		})
	}
	return t
}

// sourceView builds the candidate-source list for a title, excluding sources
// already linked.
func (u *webUI) sourceView(ctx context.Context, title library.Title, linked map[string]bool, all []sources.Source) matchView {
	if title.CatalogMangaID == nil {
		return titleSourceView(title, false, false, "", nil)
	}
	cid := *title.CatalogMangaID
	active, failed, msg := u.jobStateFor(ctx, jobs.TypeMatchSources, service.JobPayload{CatalogID: cid})
	if active {
		return titleSourceView(title, true, false, "", nil)
	}
	matches, _ := u.svc.ListMatches(ctx, cid)
	// Only offer sources that are currently usable: stored candidates from
	// sources that were disabled or deleted since must not be linkable.
	usable := map[string]bool{}
	for _, src := range all {
		if src.Enabled {
			usable[src.ID] = true
		}
	}
	kept := matches[:0]
	for _, m := range matches {
		if !linked[m.SourceID] && usable[m.SourceID] {
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
	u.page(w, r, "sources", "Sources", sourcesPageView{Sources: srcs, Scrapers: registry.Names()})
}

type sourcesPageView struct {
	Sources  []sources.Source
	Scrapers []string
}

func (u *webUI) settingsPage(w http.ResponseWriter, r *http.Request) {
	view := u.settings(r.Context())
	view.AniList = u.svc.AniListConnectionFor(r.Context(), auth.UserID(r.Context()))
	view.RedirectURL = anilistRedirectURL(r)
	view.AppConfigured = u.svc.Setting(r.Context(), service.SettingAniListClientID, "") != ""
	u.page(w, r, "settings", "Settings", view)
}

// --- search & add ---

type searchResultView struct {
	catalog.Manga
	TitleID int64 // set when the manga is already in the library
}

func (u *webUI) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("q"))
	if q == "" {
		// Cleared input: fall back to the recommendations grid.
		u.trendingManga(w, r)
		return
	}
	items, err := u.svc.SearchAniList(r.Context(), q, 12)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "mangaResults", u.mangaResultsView(r.Context(), "", resultView(r), items))
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
	if r.FormValue("card") != "" {
		u.frag(w, "addedButtonCard", title)
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
		w.Header().Set("HX-Trigger", "title-job-started") // wakes the title page's idle pollers
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
	if _, err := u.svc.RemoveTitleFiles(r.Context(), id, r.FormValue("delete_files") == "on"); err != nil {
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
	u.page(w, r, "import", "Import", nil) // candidates post-load: the dir scan can be slow
}

type importCandidatesView struct {
	Candidates []service.ImportCandidate
	Titles     []library.Title // attach targets for volume folders
}

func (u *webUI) importCandidates(w http.ResponseWriter, r *http.Request) {
	cands, err := u.svc.ExploreDownloads(r.Context())
	if err != nil {
		u.fail(w, err)
		return
	}
	titles, _ := u.svc.ListTitles(r.Context())
	titles = filterRestrictedTitles(r.Context(), titles)
	u.frag(w, "importCandidates", importCandidatesView{Candidates: cands, Titles: titles})
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
	var title library.Title
	if r.FormValue("kind") == "volumes" {
		title, err = u.svc.ImportVolumesFolder(r.Context(), r.FormValue("folder"), anilistID)
	} else {
		title, err = u.svc.ImportFolder(r.Context(), r.FormValue("folder"), anilistID)
	}
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
	allSources, _ := u.svc.ListSources(r.Context())
	u.frag(w, "matches", u.sourceView(r.Context(), title, u.linkedSourceIDs(r.Context(), id), allSources))
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

func (u *webUI) srcEnabled(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := u.svc.SetSourceEnabled(r.Context(), id, r.FormValue("on") == "true"); err != nil {
		u.fail(w, err)
		return
	}
	u.srcRow(w, r)
}

func (u *webUI) srcDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := u.svc.RemoveLocalSource(r.Context(), id); err != nil {
		u.fail(w, err)
		return
	}
	srcs, err := u.svc.ListSources(r.Context())
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "sourcesTable", srcs)
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

// sourceEditView carries the scraper choices next to the profile fields.
type sourceEditView struct {
	sources.Source
	Scrapers []string
}

func (u *webUI) srcEdit(w http.ResponseWriter, r *http.Request) {
	src, err := u.svc.GetSource(r.Context(), r.PathValue("id"))
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "sourceEdit", sourceEditView{Source: src, Scrapers: registry.Names()})
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
	p.Scraper = strings.TrimSpace(r.FormValue("scraper"))
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

// splitCommaList splits on commas only; items like tags may contain spaces.
func splitCommaList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
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
		BaseURL: base, SampleMangaURL: manga, Scraper: strings.TrimSpace(r.FormValue("scraper")),
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
	themeBefore, _ := u.theme(r.Context())
	user := userFrom(r.Context())
	for _, key := range service.SettingKeys() {
		if !r.Form.Has(key) {
			continue // section not shown to this user; leave untouched
		}
		appearance := strings.HasPrefix(key, "ui.")
		if appearance && !user.Can(auth.PermSettingsAppearance) {
			continue
		}
		if !appearance && !user.Can(auth.PermSettingsManage) {
			continue
		}
		value := strings.TrimSpace(r.FormValue(key))
		// Personal appearance values are always stored explicitly: "equals the
		// built-in default" must not clear them, or a differing pre-multi-user
		// global value (e.g. an old ui.theme) silently takes over again.
		if appearance {
			if value != "" {
				if err := service.ValidateSetting(key, value); err != nil {
					u.fail(w, err)
					return
				}
			}
			if err := u.svc.SetUserSetting(r.Context(), user.ID, key, value); err != nil {
				u.fail(w, err)
				return
			}
			continue
		}
		// Leaving a global field at its default clears any override so
		// env/config wins (avoids the pre-filled default clobbering e.g. a
		// solver endpoint set via environment).
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
	// Appearance changes need a reload to re-render with the new theme.
	if themeAfter, _ := u.theme(r.Context()); themeAfter != themeBefore || r.FormValue(service.SettingUITheme) == "custom" {
		w.Header().Set("HX-Refresh", "true")
		return
	}
	u.frag(w, "toast", toastView{OK: true, Msg: "Saved ✓"})
}

// --- helpers ---

func (u *webUI) settings(ctx context.Context) settingsView {
	cfg, _, _ := u.svc.RuntimeConfig(ctx) // effective config (env + settings merged)
	stored := u.svc.AllSettings(ctx)
	for k, v := range u.svc.UserSettings(ctx, auth.UserID(ctx)) {
		stored[k] = v // appearance keys are personal
	}
	field := func(key string) settingField {
		label, desc := settingMeta(key)
		value, ok := stored[key]
		if !ok {
			value = service.SettingDefault(key)
		}
		if cfg != nil {
			if eff, ok := effectiveSetting(cfg, key); ok {
				value = eff // show what's actually in effect (e.g. env-set endpoint)
			}
		}
		f := settingField{Key: key, Label: label, Desc: desc, Value: value}
		switch {
		case key == service.SettingUITheme:
			f.Kind, f.Options = "select", service.UIThemes()
		case strings.HasPrefix(key, "ui.custom."):
			f.Kind = "color"
		case key == service.SettingAniListClientID, key == service.SettingAniListClientSecret:
			f.Kind = "secret"
		}
		return f
	}
	fields := func(keys ...string) []settingField {
		out := make([]settingField, 0, len(keys))
		for _, k := range keys {
			out = append(out, field(k))
		}
		return out
	}
	colorKeys := make([]string, 0, len(service.CustomColorTokens()))
	for _, t := range service.CustomColorTokens() {
		colorKeys = append(colorKeys, service.CustomColorKey(t))
	}
	user := userFrom(ctx)
	var groups []settingGroup
	if user.Can(auth.PermSettingsAppearance) {
		groups = append(groups, settingGroup{Title: "Appearance", Fields: append(fields(service.SettingUITheme), fields(colorKeys...)...)})
	}
	if user.Can(auth.PermSettingsManage) {
		groups = append(groups,
			settingGroup{Title: "Scheduling", Fields: fields(
				service.SettingServeRefreshEvery, service.SettingServeScanEvery,
				service.SettingServeDownloadEvery, service.SettingServeRunEvery,
				service.SettingServeAniListSyncEvery,
				service.SettingServeCatalogEvery)},
			settingGroup{Title: "Jobs & downloads", Fields: fields(
				service.SettingJobsMaxAttempts, service.SettingJobsTimeout,
				service.SettingJobsWorkers, service.SettingDownloadsMaxAttempts,
				service.SettingRateLimitIntervalMS, service.SettingRateLimitBurst,
				service.SettingRateLimitDisabled)},
			settingGroup{Title: "Services", Fields: fields(
				service.SettingBrowserSolverEnabled, service.SettingBrowserSolverProvider,
				service.SettingBrowserSolverEndpoint, service.SettingBrowserSolverTimeoutSeconds,
				service.SettingBrowserDownloaderEnabled, service.SettingBrowserDownloaderEndpoint,
				service.SettingBrowserDownloaderTimeoutSeconds, service.SettingServicesHealthInterval)},
			settingGroup{Title: "Sources", Fields: fields(service.SettingSourceRegistryURL)},
			settingGroup{Title: "AniList application", Fields: fields(
				service.SettingAniListClientID, service.SettingAniListClientSecret)},
		)
	}
	return settingsView{Groups: groups}
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
	running, label, queued, failed, msg := titleActivityFrom(u.jobs(ctx), title)
	return activityView{Title: title, Running: running, ActiveLabel: label, Queued: queued, Failed: failed, Error: msg}
}

func titleActivityFrom(js []jobs.Job, title library.Title) (running map[string]bool, label string, queued []string, failed bool, msg string) {
	running = map[string]bool{}
	queuedSeen := map[string]bool{}
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
			// A running job shows the live spinner; merely waiting jobs are
			// listed as queued.
			if label == "" && j.Status == "running" {
				label = verb
			}
			if j.Status == "queued" && !queuedSeen[j.Type] {
				queuedSeen[j.Type] = true
				queued = append(queued, verb)
			}
		case isFailed && !failed:
			failed, msg = true, j.LastError
		}
	}
	if len(running) > 0 {
		failed = false // something is running; don't also show the last failure
	}
	return running, label, queued, failed, msg
}

func titleVerb(typ string) string {
	switch typ {
	case jobs.TypeRefreshTitle:
		return "refreshing"
	case jobs.TypeDownloadMissing:
		return "downloading"
	case jobs.TypeScanDownloads:
		return "scanning"
	case jobs.TypeAttachVolumes:
		return "attaching volumes"
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
		return "home"
	case strings.HasPrefix(path, "/management"):
		return "management"
	case strings.HasPrefix(path, "/search"):
		return "search"
	case strings.HasPrefix(path, "/library"):
		return "library"
	case strings.HasPrefix(path, "/import"):
		return "import"
	case strings.HasPrefix(path, "/sources"):
		return "sources"
	case strings.HasPrefix(path, "/users"):
		return "users"
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
		"assetVer":  func() string { return u.assetVer },
		"jobLabel":  jobLabel,
		"permLabel": func(p string) string { l, _ := permMeta(p); return l },
		"cardView": func(t library.Title, canManage bool) map[string]any {
			return map[string]any{"Title": t, "CanManage": canManage}
		},
		"permDesc": func(p string) string { _, d := permMeta(p); return d },
		"has": func(list []string, v string) bool {
			for _, s := range list {
				if s == v {
					return true
				}
			}
			return false
		},
		"mangaTitle": mangaTitle,
		"tagPicker": func(options []catalog.ContentTag, values []string) map[string]any {
			selected := make(map[string]bool, len(values))
			for _, v := range values {
				selected[v] = true
			}
			return map[string]any{"Options": options, "Selected": selected, "Values": values}
		},
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

// chapterReadPercent reports how much of a chapter has been read (0-100).
func chapterReadPercent(c library.ChapterStatus) int {
	if c.Read {
		return 100
	}
	if c.ReadPages <= 0 {
		return 0
	}
	total := c.TotalPages
	if total <= 0 {
		total = c.Pages
	}
	if total <= 0 {
		return 0
	}
	pct := c.ReadPages * 100 / total
	if pct > 99 { // not marked complete: never claim 100%
		pct = 99
	}
	return pct
}

// chapterReadTip describes read progress as pages, e.g. "7/12 pages read".
func chapterReadTip(c library.ChapterStatus) string {
	total := c.TotalPages
	if total <= 0 {
		total = c.Pages
	}
	if total <= 0 {
		return ""
	}
	read := c.ReadPages
	if c.Read {
		read = total
	}
	return fmt.Sprintf("%d/%d pages read", read, total)
}

// chapterRowClass tints a chapter row by download state.
func chapterRowClass(c library.ChapterStatus) string {
	switch {
	case c.Downloaded:
		return "bg-success/5"
	case c.Failed:
		return "bg-error/5"
	default:
		return "opacity-50"
	}
}

func filterChapters(chs []library.ChapterStatus, q string) []library.ChapterStatus {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return chs
	}
	out := chs[:0]
	for _, c := range chs {
		if strings.Contains(strings.ToLower(c.Label), q) || strings.Contains(strings.ToLower(c.Title), q) {
			out = append(out, c)
		}
	}
	return out
}

func chapterTableParams(values url.Values) url.Values {
	out := url.Values{}
	if q := strings.TrimSpace(values.Get("q")); q != "" {
		out.Set("q", q)
	}
	if values.Get("dir") == "desc" {
		out.Set("dir", "desc")
	}
	if o := values.Get("order"); o != "" {
		out.Set("order", o)
	}
	return out
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
	case jobs.TypeSyncAniList:
		return "AniList sync"
	case jobs.TypeCatalogRefresh:
		return "Catalog refresh"
	case jobs.TypeAttachVolumes:
		return "Attach volumes"
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
