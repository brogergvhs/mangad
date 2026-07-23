package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brogergvhs/kaodoku/internal/auth"
	"github.com/brogergvhs/kaodoku/internal/catalog"
	"github.com/brogergvhs/kaodoku/internal/jobs"
	"github.com/brogergvhs/kaodoku/internal/library"
	"github.com/brogergvhs/kaodoku/internal/service"
)

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", "invalid json")
		return false
	}
	return true
}

func (a *apiV1) guardedTitleID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := pathID(r)
	if err != nil || !titleAllowed(r.Context(), a.svc, id) {
		v1err(w, http.StatusNotFound, "not_found", "title not found")
		return 0, false
	}
	return id, true
}

func (a *apiV1) setFavourite(want bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := a.guardedTitleID(w, r)
		if !ok {
			return
		}
		t, err := a.svc.GetTitle(r.Context(), id)
		if err != nil {
			v1err(w, http.StatusNotFound, "not_found", "title not found")
			return
		}
		if t.Favourite != want {
			if _, err := a.svc.ToggleFavourite(r.Context(), id); err != nil {
				v1err(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]bool{"favourite": want})
	}
}

func (a *apiV1) patchTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := a.guardedTitleID(w, r)
	if !ok {
		return
	}
	var body struct {
		Monitored       *bool   `json:"monitored"`
		LanguageMode    *string `json:"language_mode"`
		RefreshInterval *string `json:"refresh_interval"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Monitored != nil {
		if err := a.svc.SetMonitored(r.Context(), id, *body.Monitored); err != nil {
			v1err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if body.LanguageMode != nil {
		if err := a.svc.SetLanguageMode(r.Context(), id, *body.LanguageMode); err != nil {
			v1err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if body.RefreshInterval != nil {
		if err := a.svc.SetRefreshInterval(r.Context(), id, *body.RefreshInterval); err != nil {
			v1err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	t, err := a.svc.GetTitle(r.Context(), id)
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toTitleDTO(t))
}

func (a *apiV1) deleteTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := a.guardedTitleID(w, r)
	if !ok {
		return
	}
	deleteFiles := r.URL.Query().Get("delete_files") == "1"
	deleteAniList := r.URL.Query().Get("delete_anilist") == "1"
	if _, err := a.svc.RemoveTitleFiles(r.Context(), id, deleteFiles, deleteAniList); err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiV1) volumeSetRead(read bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			v1err(w, http.StatusBadRequest, "bad_request", "invalid id")
			return
		}
		vol, err := a.svc.GetVolume(r.Context(), id)
		if err != nil || !titleAllowed(r.Context(), a.svc, vol.TitleID) {
			v1err(w, http.StatusNotFound, "not_found", "volume not found")
			return
		}
		if err := a.svc.SetVolumeRead(r.Context(), id, read); err != nil {
			v1err(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		vol, _ = a.svc.GetVolume(r.Context(), id)
		writeJSON(w, http.StatusOK, toVolumeDTO(vol))
	}
}

func (a *apiV1) volumesReadRange(w http.ResponseWriter, r *http.Request) {
	id, ok := a.guardedTitleID(w, r)
	if !ok {
		return
	}
	var body struct {
		From float64 `json:"from"`
		To   float64 `json:"to"`
		Read bool    `json:"read"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	n, err := a.svc.SetVolumeRangeRead(r.Context(), id, body.From, body.To, body.Read)
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"volumes": n})
}

func (a *apiV1) ownsCollection(r *http.Request, id int64) bool {
	cols, err := a.svc.CustomCollections(r.Context())
	if err != nil {
		return false
	}
	for _, c := range cols {
		if c.ID == id {
			return true
		}
	}
	return false
}

func (a *apiV1) collectionsList(w http.ResponseWriter, r *http.Request) {
	cols, err := a.svc.CustomCollections(r.Context())
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	items := make([]collectionDTO, 0, len(cols))
	for _, c := range cols {
		items = append(items, collectionDTO{ID: c.ID, Name: c.Name, Kind: "manual"})
	}
	pins, _ := a.svc.SmartPins(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "smart_pins": pins})
}

func (a *apiV1) collectionCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	id, err := a.svc.CreateCollection(r.Context(), body.Name)
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, collectionDTO{ID: id, Name: body.Name, Kind: "manual"})
}

func (a *apiV1) collectionGet(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	cols, err := a.svc.CustomCollections(r.Context())
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	for _, c := range cols {
		if c.ID == id {
			members, _ := a.svc.CollectionMembers(r.Context())
			writeJSON(w, http.StatusOK, collectionDTO{ID: c.ID, Name: c.Name, Kind: "manual", TitleIDs: members[id]})
			return
		}
	}
	v1err(w, http.StatusNotFound, "not_found", "collection not found")
}

func (a *apiV1) collectionPatch(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if !a.ownsCollection(r, id) {
		v1err(w, http.StatusNotFound, "not_found", "collection not found")
		return
	}
	if err := a.svc.RenameCollection(r.Context(), id, body.Name); err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, collectionDTO{ID: id, Name: body.Name, Kind: "manual"})
}

func (a *apiV1) collectionDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if !a.ownsCollection(r, id) {
		v1err(w, http.StatusNotFound, "not_found", "collection not found")
		return
	}
	if err := a.svc.DeleteCollection(r.Context(), id); err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiV1) collectionMember(add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseInt64Path(r, "id")
		titleID, err2 := parseInt64Path(r, "titleId")
		if err != nil || err2 != nil {
			v1err(w, http.StatusBadRequest, "bad_request", "invalid id")
			return
		}
		if !titleAllowed(r.Context(), a.svc, titleID) {
			v1err(w, http.StatusNotFound, "not_found", "title not found")
			return
		}
		fn := a.svc.RemoveFromCollection
		if add {
			fn = a.svc.AddToCollection
		}
		if err := fn(r.Context(), id, titleID); err != nil {
			v1err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *apiV1) smartPin(add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		titleID, err := parseInt64Path(r, "titleId")
		if key == "" || err != nil {
			v1err(w, http.StatusBadRequest, "bad_request", "invalid key or id")
			return
		}
		if !titleAllowed(r.Context(), a.svc, titleID) {
			v1err(w, http.StatusNotFound, "not_found", "title not found")
			return
		}
		fn := a.svc.RemoveSmartPin
		if add {
			fn = a.svc.PinToSmart
		}
		if err := fn(r.Context(), key, titleID); err != nil {
			v1err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *apiV1) screensList(w http.ResponseWriter, r *http.Request) {
	screens, err := a.svc.Screens(r.Context())
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	items := make([]screenDTO, 0, len(screens))
	for _, s := range screens {
		items = append(items, toScreenDTO(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *apiV1) screenSave(w http.ResponseWriter, r *http.Request) {
	var body screenDTO
	if !decodeBody(w, r, &body) {
		return
	}
	body.ID = 0
	if r.Method == http.MethodPatch {
		id, err := pathID(r)
		if err != nil {
			v1err(w, http.StatusBadRequest, "bad_request", "invalid id")
			return
		}
		if _, err := a.svc.GetScreen(r.Context(), id); err != nil {
			v1err(w, http.StatusNotFound, "not_found", "screen not found")
			return
		}
		body.ID = id
	}
	id, err := a.svc.SaveScreen(r.Context(), library.Screen{ID: body.ID, Name: body.Name, Config: body.Config})
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	body.ID = id
	writeJSON(w, http.StatusOK, body)
}

func (a *apiV1) screenDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, err := a.svc.GetScreen(r.Context(), id); err != nil {
		v1err(w, http.StatusNotFound, "not_found", "screen not found")
		return
	}
	if err := a.svc.DeleteScreen(r.Context(), id); err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiV1) screensReorder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := a.svc.ReorderScreens(r.Context(), body.IDs); err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiV1) meSettingsGet(w http.ResponseWriter, r *http.Request) {
	stored := a.svc.UserSettings(r.Context(), userFrom(r.Context()).ID)
	out := userSettingsDTO{
		ReaderMode: stored["reader.mode"], ReaderDir: stored["reader.dir"],
		ReaderFit: stored["reader.fit"], Theme: stored[service.SettingUITheme],
	}
	if z, err := strconv.ParseFloat(stored["reader.zoom"], 64); err == nil {
		out.ReaderZoom = &z
	}
	writeJSON(w, http.StatusOK, out)
}

// meSettingsPut validates everything before writing so a bad field can't leave
// a half-applied update; empty values clear the stored setting.
func (a *apiV1) meSettingsPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ReaderMode *string  `json:"reader_mode"`
		ReaderDir  *string  `json:"reader_dir"`
		ReaderFit  *string  `json:"reader_fit"`
		ReaderZoom *float64 `json:"reader_zoom"`
		Theme      *string  `json:"theme"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	writes := map[string]string{}
	if body.ReaderMode != nil {
		writes["reader.mode"] = *body.ReaderMode
	}
	if body.ReaderDir != nil {
		writes["reader.dir"] = *body.ReaderDir
	}
	if body.ReaderFit != nil {
		writes["reader.fit"] = *body.ReaderFit
	}
	if body.ReaderZoom != nil {
		writes["reader.zoom"] = strconv.FormatFloat(*body.ReaderZoom, 'f', -1, 64)
	}
	if body.Theme != nil {
		if *body.Theme != "" {
			if err := service.ValidateSetting(service.SettingUITheme, *body.Theme); err != nil {
				v1err(w, http.StatusBadRequest, "bad_request", err.Error())
				return
			}
		}
		writes[service.SettingUITheme] = *body.Theme
	}
	userID := userFrom(r.Context()).ID
	for key, value := range writes {
		if err := a.svc.SetUserSetting(r.Context(), userID, key, value); err != nil {
			v1err(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
	}
	a.meSettingsGet(w, r)
}

func (a *apiV1) anilistStatus(w http.ResponseWriter, r *http.Request) {
	conn := a.svc.AniListConnectionFor(r.Context(), userFrom(r.Context()).ID)
	writeJSON(w, http.StatusOK, aniListDTO{Connected: conn.Connected, Name: conn.Name, ExpiresAt: conn.ExpiresAt})
}

func (a *apiV1) anilistSync(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TitleID int64 `json:"title_id"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decodeBody(w, r, &body) {
		return
	}
	var err error
	if body.TitleID > 0 {
		if !titleAllowed(r.Context(), a.svc, body.TitleID) {
			v1err(w, http.StatusNotFound, "not_found", "title not found")
			return
		}
		err = a.svc.SyncAniListTitle(r.Context(), body.TitleID)
	} else {
		err = a.svc.EnqueueAniListSync(r.Context(), userFrom(r.Context()).ID)
	}
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *apiV1) anilistDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.DisconnectAniList(r.Context(), userFrom(r.Context()).ID); err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiV1) wantedSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	term := strings.TrimSpace(q.Get("q"))
	if term == "" {
		v1err(w, http.StatusBadRequest, "bad_request", "q is required")
		return
	}
	filter := catalog.SearchFilter{
		GenreIn: splitCSV(q.Get("genre_in")), GenreNotIn: splitCSV(q.Get("genre_not_in")),
		TagIn: splitCSV(q.Get("tag_in")), TagNotIn: splitCSV(q.Get("tag_not_in")),
	}
	filter.Page, _ = strconv.Atoi(q.Get("page"))
	items, hasMore, err := a.svc.SearchAniList(r.Context(), term, clampLimit(q.Get("limit")), filter)
	if err != nil {
		v1err(w, http.StatusBadGateway, "unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": allowedManga(r.Context(), items), "has_more": hasMore})
}

func (a *apiV1) wantedTrending(w http.ResponseWriter, r *http.Request) {
	items, err := a.svc.TrendingManga(r.Context(), clampLimit(r.URL.Query().Get("limit")))
	if err != nil {
		v1err(w, http.StatusBadGateway, "unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": allowedManga(r.Context(), items)})
}

func (a *apiV1) wantedList(w http.ResponseWriter, r *http.Request) {
	items, err := a.svc.ListWanted(r.Context())
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": allowedManga(r.Context(), items)})
}

func (a *apiV1) wantedAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AniListID int `json:"anilist_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	item, err := a.svc.AddAniListWanted(r.Context(), body.AniListID, func(m catalog.Manga) bool {
		return contentAllowed(r.Context(), m.IsAdult, mangaContentTags(m))
	})
	if errors.Is(err, service.ErrContentBlocked) {
		v1err(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err != nil {
		v1err(w, http.StatusBadGateway, "unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (a *apiV1) guardedManga(w http.ResponseWriter, r *http.Request, catalogID int64) bool {
	if m, err := a.svc.GetManga(r.Context(), catalogID); err != nil || !contentAllowed(r.Context(), m.IsAdult, mangaContentTags(m)) {
		v1err(w, http.StatusNotFound, "not_found", "not found")
		return false
	}
	return true
}

func (a *apiV1) matchesList(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64Query(r, "catalog_id")
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if !a.guardedManga(w, r, id) {
		return
	}
	matches, err := a.svc.ListMatches(r.Context(), id)
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": matches})
}

func (a *apiV1) matchesFind(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CatalogID int64 `json:"catalog_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if !a.guardedManga(w, r, body.CatalogID) {
		return
	}
	matches, err := a.svc.MatchSources(r.Context(), body.CatalogID)
	if err != nil {
		v1err(w, http.StatusBadGateway, "unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": matches})
}

func (a *apiV1) track(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MatchID         int64  `json:"match_id"`
		Output          string `json:"output"`
		Monitored       *bool  `json:"monitored"`
		RefreshInterval string `json:"refresh_interval"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	match, err := a.svc.GetMatch(r.Context(), body.MatchID)
	if err != nil || !a.guardedManga(w, r, match.CatalogMangaID) {
		if err != nil {
			v1err(w, http.StatusNotFound, "not_found", "match not found")
		}
		return
	}
	monitored := body.Monitored == nil || *body.Monitored
	t, err := a.svc.TrackMatch(r.Context(), body.MatchID, body.Output, monitored, body.RefreshInterval)
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toTitleDTO(t))
}

func (a *apiV1) jobsList(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.List(r.Context())
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	q := r.URL.Query()
	status, typ := q.Get("status"), q.Get("type")
	items := make([]jobDTO, 0, len(list))
	for _, j := range list {
		if (status == "" || j.Status == status) && (typ == "" || j.Type == typ) {
			items = append(items, toJobDTO(j))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *apiV1) jobGet(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	j, err := a.svc.GetJob(r.Context(), id)
	if err != nil {
		v1err(w, http.StatusNotFound, "not_found", "job not found")
		return
	}
	writeJSON(w, http.StatusOK, toJobDTO(j))
}

// titleScopedJobTypes may be enqueued with library.manage by the title's owner.
var titleScopedJobTypes = map[string]bool{
	jobs.TypeRefreshTitle:    true,
	jobs.TypeDownloadMissing: true,
	jobs.TypeScanDownloads:   true,
}

// mayEnqueue enforces the split: jobs.manage for anything, library.manage only
// for title-scoped jobs on titles the user added (added_by 0 = env admin).
func (a *apiV1) mayEnqueue(r *http.Request, typ string, titleID int64) bool {
	user := userFrom(r.Context())
	if user.Can(auth.PermJobsManage) {
		return true
	}
	if !user.Can(auth.PermLibraryManage) || !titleScopedJobTypes[typ] || titleID <= 0 {
		return false
	}
	owners, err := a.svc.TitleOwners(r.Context())
	if err != nil {
		return false
	}
	owner := owners[titleID]
	if owner == 0 {
		owner = auth.EnvAdminID
	}
	return owner == user.ID && titleAllowed(r.Context(), a.svc, titleID)
}

func (a *apiV1) jobEnqueue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type      string `json:"type"`
		TitleID   int64  `json:"title_id"`
		SourceID  string `json:"source_id"`
		CatalogID int64  `json:"catalog_id"`
		Delay     string `json:"delay"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if !a.mayEnqueue(r, body.Type, body.TitleID) {
		v1err(w, http.StatusForbidden, "forbidden", "missing permission for this job")
		return
	}
	runAfter := time.Now()
	if body.Delay != "" {
		d, err := time.ParseDuration(body.Delay)
		if err != nil || d < 0 || d > 24*time.Hour {
			v1err(w, http.StatusBadRequest, "bad_request", "invalid delay")
			return
		}
		runAfter = runAfter.Add(d)
	}
	var j jobs.Job
	var err error
	switch body.Type {
	case jobs.TypeVerifySource:
		j, err = a.svc.EnqueueSource(r.Context(), body.SourceID, runAfter)
	case jobs.TypeMatchSources:
		j, err = a.svc.EnqueueCatalog(r.Context(), body.Type, body.CatalogID, runAfter)
	default:
		j, err = a.svc.Enqueue(r.Context(), body.Type, body.TitleID, runAfter)
	}
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toJobDTO(j))
}

func jobsRunV1(runJobs func(context.Context) (service.RunSummary, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sum, err := runJobs(r.Context())
		if err != nil {
			v1err(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"done": sum.Done, "failed": sum.Failed})
	}
}

func (a *apiV1) notificationsList(w http.ResponseWriter, r *http.Request) {
	sc := notificationScope(userFrom(r.Context()))
	items, err := a.svc.Notifications(r.Context(), sc, clampLimit(r.URL.Query().Get("limit")))
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	unread, _ := a.svc.UnreadNotificationCount(r.Context(), sc)
	out := make([]notificationDTO, 0, len(items))
	for _, n := range items {
		out = append(out, toNotificationDTO(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "unread": unread})
}

func (a *apiV1) notificationsRead(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.MarkNotificationsRead(r.Context(), notificationScope(userFrom(r.Context()))); err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *apiV1) notificationDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		v1err(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if err := a.svc.DeleteNotification(r.Context(), id); err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sourcesPick lists the minimal source picker for users without sources.manage.
func (a *apiV1) sourcesPick(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.SyncSources(r.Context(), ""); err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	list, err := a.svc.ListSources(r.Context())
	if err != nil {
		v1err(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	list = filterNSFWSources(r.Context(), list)
	items := make([]sourcePickDTO, 0, len(list))
	for _, s := range list {
		items = append(items, toSourcePickDTO(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
