package server

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/brogergvhs/mangad/internal/auth"
	"github.com/brogergvhs/mangad/internal/service"
)

// userFrom returns the authenticated user for a request (never nil once the
// auth middleware has run).
func userFrom(ctx context.Context) *auth.User { return auth.FromContext(ctx) }

// authEnabled reports whether username/password auth is active.
func authEnabled() bool { return os.Getenv("MANGAD_ADMIN_PASSWORD") != "" }

// requireUser resolves the request's user and stores it in the context:
// session cookie when auth is enabled, the env admin otherwise.
func requireUser(next http.Handler, svc *service.JobService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		user := resolveUser(r, svc)
		if user == nil {
			if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if p := requiredPerm(r); p != "" && !user.Can(p) {
			writeError(w, http.StatusForbidden, "missing permission: "+p)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
	})
}

// requiredPerm maps a request to the permission it needs. Central and
// deny-by-default: unknown mutations require library.manage.
func requiredPerm(r *http.Request) string {
	p := r.URL.Path
	switch {
	case p == "/logout":
		return "" // any signed-in user
	case strings.HasPrefix(p, "/reader/") || strings.HasPrefix(p, "/api/reader/"):
		return auth.PermReaderUse
	case p == "/users" || strings.HasPrefix(p, "/ui/users"):
		return auth.PermUsersManage
	case p == "/settings" || p == "/ui/settings" || p == "/api/settings":
		return auth.PermSettingsManage
	case p == "/sources" || strings.HasPrefix(p, "/ui/sources"):
		return auth.PermSourcesManage
	case strings.HasPrefix(p, "/ui/jobs/") && r.Method != http.MethodGet:
		return auth.PermJobsManage
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		return auth.PermLibraryView
	case strings.Contains(p, "/chapters/"): // read/unread marking, bulk range
		return auth.PermReaderUse
	default:
		return auth.PermLibraryManage
	}
}

func resolveUser(r *http.Request, svc *service.JobService) *auth.User {
	ctx := r.Context()
	if !authEnabled() {
		// Single-user mode: everything acts as the env admin.
		admin, _ := svc.Auth().GetUser(ctx, auth.EnvAdminID)
		return admin
	}
	if c, err := r.Cookie("mangad_session"); err == nil {
		if user, err := svc.Auth().UserBySession(ctx, c.Value); err == nil && user != nil {
			return user
		}
	}
	return nil
}

const loginPage = `<!doctype html><html lang="en" data-theme="mocha"><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in · MangaD</title><link rel="stylesheet" href="/static/app.css">
<body class="grid min-h-screen place-items-center bg-base-200">
<form class="card bg-base-100 card-body w-80 gap-3" method="post" action="/login">
<h1 class="text-xl font-bold">Manga<span class="text-primary">D</span></h1>
%s
<input class="input w-full" name="username" placeholder="Username" autofocus autocomplete="username">
<input class="input w-full" type="password" name="password" placeholder="Password" autocomplete="current-password">
<button class="btn btn-primary w-full">Sign in</button>
</form></body></html>`

func registerAuthRoutes(mux *http.ServeMux, svc *service.JobService) {
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Replace(loginPage, "%s", "", 1)))
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		token, err := svc.Auth().Login(r.Context(), r.FormValue("username"), r.FormValue("password"))
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(strings.Replace(loginPage, "%s", `<p class="text-sm text-error">Invalid username or password.</p>`, 1)))
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: "mangad_session", Value: token, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode,
			MaxAge: 30 * 24 * 3600,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("mangad_session"); err == nil {
			svc.Auth().Logout(r.Context(), c.Value)
		}
		http.SetCookie(w, &http.Cookie{Name: "mangad_session", Value: "", Path: "/", MaxAge: -1})
		w.Header().Set("HX-Redirect", "/login")
	})
}
