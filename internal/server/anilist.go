package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"

	"github.com/brogergvhs/kaodoku/internal/auth"
	"github.com/brogergvhs/kaodoku/internal/catalog"
	"github.com/brogergvhs/kaodoku/internal/service"
)

// anilistRedirectURL is the callback the admin registers on their AniList app.
func anilistRedirectURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || forwardedProto(r) == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/anilist/callback", scheme, requestHost(r))
}

func (u *webUI) anilistConnect(w http.ResponseWriter, r *http.Request) {
	clientID := u.svc.Setting(r.Context(), service.SettingAniListClientID, "")
	if clientID == "" {
		http.Error(w, "the AniList application is not configured yet (settings → AniList)", http.StatusBadRequest)
		return
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	state := hex.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{Name: "anilist_state", Value: state, Path: "/", MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", anilistRedirectURL(r))
	q.Set("response_type", "code")
	q.Set("state", state)
	http.Redirect(w, r, "https://anilist.co/api/v2/oauth/authorize?"+q.Encode(), http.StatusSeeOther)
}

func (u *webUI) anilistCallback(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("anilist_state"); err != nil || c.Value == "" || c.Value != r.URL.Query().Get("state") {
		http.Error(w, "state mismatch — restart the AniList connection from settings", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "anilist_state", Value: "", Path: "/", MaxAge: -1})
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "AniList did not return an authorization code", http.StatusBadRequest)
		return
	}
	user := userFrom(r.Context())
	if err := u.svc.ConnectAniList(r.Context(), user.ID, anilistRedirectURL(r), code); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (u *webUI) anilistLibrary(w http.ResponseWriter, r *http.Request) {
	entries, err := u.svc.AniListLibrary(r.Context())
	if err != nil {
		u.fail(w, err)
		return
	}
	items := make([]catalog.Manga, 0, len(entries))
	for _, e := range entries {
		items = append(items, e.Manga)
	}
	u.frag(w, "mangaResults", u.mangaResultsView(r.Context(), "", "cards", items))
}

// anilistSuggestions lists the user's AniList entries that aren't in the
// library yet, as a collapsible in the page's current view. Renders nothing
// when disconnected, on API failure, or with nothing missing.
func (u *webUI) anilistSuggestions(w http.ResponseWriter, r *http.Request) {
	entries, err := u.svc.AniListLibrary(r.Context())
	if err != nil {
		u.frag(w, "anilistSuggestions", mangaResults{})
		return
	}
	items := make([]catalog.Manga, 0, len(entries))
	for _, e := range entries {
		if e.Status == "DROPPED" { // usually a title deliberately removed here
			continue
		}
		items = append(items, e.Manga)
	}
	view := u.mangaResultsView(r.Context(), "", resultView(r), items)
	missing := view.Items[:0]
	for _, it := range view.Items {
		if it.TitleID == 0 {
			missing = append(missing, it)
		}
	}
	view.Items = missing
	u.frag(w, "anilistSuggestions", view)
}

func (u *webUI) anilistSyncNow(w http.ResponseWriter, r *http.Request) {
	if err := u.svc.EnqueueAniListSync(r.Context(), auth.UserID(r.Context())); err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	u.frag(w, "toast", toastView{OK: true, Msg: "AniList sync queued ✓"})
}

// anilistSyncTitle reconciles one title with the acting user's AniList list.
func (u *webUI) anilistSyncTitle(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64Path(r, "id")
	if err != nil {
		u.fail(w, err)
		return
	}
	if !titleAllowed(r.Context(), u.svc, id) {
		http.NotFound(w, r)
		return
	}
	if err := u.svc.SyncAniListTitle(r.Context(), id); err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "toast", toastView{OK: true, Msg: "AniList synced ✓"})
}

func (u *webUI) anilistDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := u.svc.DisconnectAniList(r.Context(), auth.UserID(r.Context())); err != nil {
		u.fail(w, err)
		return
	}
	w.Header().Set("HX-Refresh", "true")
}
