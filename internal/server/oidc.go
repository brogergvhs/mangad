package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/brogergvhs/kaodoku/internal/auth"
	"github.com/brogergvhs/kaodoku/internal/service"
)

// OIDC single sign-on: one IdP configured via environment, users linked by
// subject. The env-admin password login stays as break-glass.
type oidcSettings struct {
	issuer        string
	clientID      string
	clientSecret  string
	redirectURL   string
	usernameClaim string
	roleClaim     string
	roleMap       map[string]string // IdP group/claim value -> kaodoku role name
	defaultRole   string
	autoProvision bool
}

func oidcConfig() oidcSettings {
	cfg := oidcSettings{
		issuer:        strings.TrimSpace(os.Getenv("KAODOKU_OIDC_ISSUER")),
		clientID:      strings.TrimSpace(os.Getenv("KAODOKU_OIDC_CLIENT_ID")),
		clientSecret:  os.Getenv("KAODOKU_OIDC_CLIENT_SECRET"),
		redirectURL:   strings.TrimSpace(os.Getenv("KAODOKU_OIDC_REDIRECT_URL")),
		usernameClaim: strings.TrimSpace(os.Getenv("KAODOKU_OIDC_USERNAME_CLAIM")),
		roleClaim:     strings.TrimSpace(os.Getenv("KAODOKU_OIDC_ROLE_CLAIM")),
		roleMap:       map[string]string{},
		defaultRole:   strings.TrimSpace(os.Getenv("KAODOKU_OIDC_DEFAULT_ROLE")),
		autoProvision: os.Getenv("KAODOKU_OIDC_AUTO_PROVISION") == "true",
	}
	if cfg.usernameClaim == "" {
		cfg.usernameClaim = "preferred_username"
	}
	if cfg.defaultRole == "" {
		cfg.defaultRole = "member"
	}
	for _, pair := range strings.Split(os.Getenv("KAODOKU_OIDC_ROLE_MAP"), ",") {
		if k, v, ok := strings.Cut(pair, ":"); ok {
			cfg.roleMap[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return cfg
}

func oidcEnabled() bool {
	cfg := oidcConfig()
	return authEnabled() && cfg.issuer != "" && cfg.clientID != ""
}

// oidcProviders caches issuer discovery so a slow IdP never blocks startup
// and repeated logins skip rediscovery.
var oidcProviders sync.Map

func oidcProvider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	if p, ok := oidcProviders.Load(issuer); ok {
		return p.(*oidc.Provider), nil
	}
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	p, err := oidc.NewProvider(dctx, issuer)
	if err != nil {
		return nil, err
	}
	oidcProviders.Store(issuer, p)
	return p, nil
}

func oidcOAuthConfig(r *http.Request, cfg oidcSettings, provider *oidc.Provider) *oauth2.Config {
	redirect := cfg.redirectURL
	if redirect == "" {
		scheme := "http"
		if secureRequest(r) {
			scheme = "https"
		}
		redirect = scheme + "://" + requestHost(r) + "/auth/oidc/callback"
	}
	scopes := []string{oidc.ScopeOpenID, "profile", "email"}
	if extra := strings.Fields(os.Getenv("KAODOKU_OIDC_SCOPES")); len(extra) > 0 {
		scopes = append([]string{oidc.ScopeOpenID}, extra...)
	}
	return &oauth2.Config{
		ClientID:     cfg.clientID,
		ClientSecret: cfg.clientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  redirect,
		Scopes:       scopes,
	}
}

const oidcStateCookie = "kaodoku_oidc_state"

// oidcStateCookieFor upgrades to a __Host- cookie on HTTPS so sibling
// subdomains cannot plant login state.
func oidcStateCookieFor(r *http.Request) (name, path string) {
	if secureRequest(r) {
		return "__Host-" + oidcStateCookie, "/"
	}
	return oidcStateCookie, "/auth/oidc"
}

// oidcFail logs the detail and shows the login page with a readable notice —
// these errors land in a browser, not an API client.
func oidcFail(w http.ResponseWriter, status int, public string, err error) {
	if err != nil {
		log.Printf("oidc: %s: %v", public, err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(renderLoginPage(`<p class="text-sm text-error">` + html.EscapeString(public) + `</p>`)))
}

func oidcLogin(w http.ResponseWriter, r *http.Request) {
	cfg := oidcConfig()
	if !oidcEnabled() {
		http.NotFound(w, r)
		return
	}
	provider, err := oidcProvider(r.Context(), cfg.issuer)
	if err != nil {
		oidcFail(w, http.StatusServiceUnavailable, "The identity provider is unavailable — try again later.", err)
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		oidcFail(w, http.StatusInternalServerError, "Sign-in could not be started.", err)
		return
	}
	state, nonce := hex.EncodeToString(raw[:16]), hex.EncodeToString(raw[16:])
	name, path := oidcStateCookieFor(r)
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: state + "." + nonce, Path: path,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: secureRequest(r), MaxAge: 600,
	})
	http.Redirect(w, r, oidcOAuthConfig(r, cfg, provider).AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusSeeOther)
}

func oidcCallback(svc *service.JobService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := oidcConfig()
		if !oidcEnabled() {
			http.NotFound(w, r)
			return
		}
		name, path := oidcStateCookieFor(r)
		cookie, err := r.Cookie(name)
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: path, Secure: secureRequest(r), MaxAge: -1})
		if denied := r.URL.Query().Get("error"); denied != "" {
			oidcFail(w, http.StatusUnauthorized, "Sign-in was cancelled or denied by the identity provider.",
				fmt.Errorf("%s: %s", denied, r.URL.Query().Get("error_description")))
			return
		}
		state, nonce, ok := strings.Cut(valueOf(cookie), ".")
		if err != nil || !ok || state == "" || nonce == "" || r.URL.Query().Get("state") != state {
			oidcFail(w, http.StatusBadRequest, "Sign-in state mismatch — please try again.", err)
			return
		}
		provider, err := oidcProvider(r.Context(), cfg.issuer)
		if err != nil {
			oidcFail(w, http.StatusServiceUnavailable, "The identity provider is unavailable — try again later.", err)
			return
		}
		token, err := oidcOAuthConfig(r, cfg, provider).Exchange(r.Context(), r.URL.Query().Get("code"))
		if err != nil {
			oidcFail(w, http.StatusUnauthorized, "Sign-in failed at the identity provider.", err)
			return
		}
		rawID, _ := token.Extra("id_token").(string)
		idToken, err := provider.Verifier(&oidc.Config{ClientID: cfg.clientID}).Verify(r.Context(), rawID)
		if err != nil {
			oidcFail(w, http.StatusUnauthorized, "The identity token was rejected.", err)
			return
		}
		if idToken.Nonce != nonce {
			oidcFail(w, http.StatusUnauthorized, "The identity token was rejected.", fmt.Errorf("nonce mismatch"))
			return
		}
		var claims map[string]any
		if err := idToken.Claims(&claims); err != nil {
			oidcFail(w, http.StatusUnauthorized, "The identity token was rejected.", err)
			return
		}

		user, err := oidcResolveUser(r.Context(), svc, cfg, idToken.Subject, claims)
		if err != nil {
			oidcFail(w, http.StatusForbidden, err.Error(), nil)
			return
		}
		session, err := svc.Auth().IssueSession(r.Context(), user.ID, r.UserAgent(), clientIP(r))
		if err != nil {
			oidcFail(w, http.StatusInternalServerError, "Sign-in could not be completed.", err)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: "kaodoku_session", Value: session, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode,
			Secure: secureRequest(r),
			MaxAge: 30 * 24 * 3600,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// oidcResolveUser maps a verified subject to a kaodoku user: linked users are
// found by subject (role synced from the claim map), unknown subjects are
// provisioned when auto-provisioning is on.
func oidcResolveUser(ctx context.Context, svc *service.JobService, cfg oidcSettings, subject string, claims map[string]any) (*auth.User, error) {
	user, err := svc.Auth().UserByOIDCSubject(ctx, subject)
	if err != nil {
		return nil, err
	}
	mappedRole := oidcMappedRole(cfg, claims)
	if cfg.roleClaim != "" && mappedRole == "" {
		mappedRole = cfg.defaultRole // removed from every mapped group: revoke
	}
	if user != nil {
		if mappedRole != "" && !user.IsEnvAdmin() && !strings.EqualFold(user.RoleName, mappedRole) {
			id, ok := roleIDByName(ctx, svc, mappedRole)
			if !ok {
				return nil, fmt.Errorf("role %q from the identity provider does not exist", mappedRole)
			}
			if err := svc.Auth().SetUserRole(ctx, user.ID, id); err != nil {
				return nil, fmt.Errorf("role sync failed")
			}
			user, err = svc.Auth().GetUser(ctx, user.ID)
			if err != nil {
				return nil, err
			}
		}
		return user, nil
	}
	if !cfg.autoProvision {
		return nil, fmt.Errorf("this identity is not linked to a kaodoku account — an admin can link subject %q from the Users page", subject)
	}
	roleName := mappedRole
	if roleName == "" {
		roleName = cfg.defaultRole
	}
	roleID, ok := roleIDByName(ctx, svc, roleName)
	if !ok {
		return nil, fmt.Errorf("role %q not found for auto-provisioning", roleName)
	}
	username := oidcUsername(cfg, subject, claims)
	id, err := svc.Auth().CreateOIDCUser(ctx, username, subject, roleID)
	if err != nil {
		if existing, lookupErr := svc.Auth().UserByOIDCSubject(ctx, subject); lookupErr == nil && existing != nil {
			return existing, nil // concurrent first login won the race
		}
		suffix := subject
		if len(suffix) > 6 {
			suffix = suffix[:6]
		}
		if id, err = svc.Auth().CreateOIDCUser(ctx, username+"-"+suffix, subject, roleID); err != nil {
			log.Printf("oidc: provisioning %q failed: %v", username, err)
			return nil, fmt.Errorf("your account could not be provisioned — ask an admin to check the server log")
		}
	}
	return svc.Auth().GetUser(ctx, id)
}

func oidcMappedRole(cfg oidcSettings, claims map[string]any) string {
	if cfg.roleClaim == "" {
		return ""
	}
	for _, v := range claimStrings(claimAt(claims, cfg.roleClaim)) {
		if role, ok := cfg.roleMap[v]; ok {
			return role
		}
	}
	return ""
}

// claimAt resolves a dotted path, so Keycloak's realm_access.roles works.
func claimAt(claims map[string]any, path string) any {
	var v any = claims
	for _, key := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

func oidcUsername(cfg oidcSettings, subject string, claims map[string]any) string {
	for _, key := range []string{cfg.usernameClaim, "preferred_username", "email", "name"} {
		if vals := claimStrings(claims[key]); len(vals) > 0 && strings.TrimSpace(vals[0]) != "" {
			return strings.TrimSpace(vals[0])
		}
	}
	return subject
}

func claimStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func roleIDByName(ctx context.Context, svc *service.JobService, name string) (int64, bool) {
	roles, err := svc.Auth().ListRoles(ctx)
	if err != nil {
		return 0, false
	}
	for _, role := range roles {
		if strings.EqualFold(role.Name, name) {
			return role.ID, true
		}
	}
	return 0, false
}

func valueOf(c *http.Cookie) string {
	if c == nil {
		return ""
	}
	return c.Value
}
