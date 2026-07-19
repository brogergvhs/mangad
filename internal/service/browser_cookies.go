package service

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/brogergvhs/kaodoku/internal/database"
)

const sessionCookieTTL = 12 * time.Hour

type browserCookieCache struct {
	dbPath string
}

type cachedBrowserSession struct {
	cookies   []*http.Cookie
	userAgent string
}

func (c browserCookieCache) load(ctx context.Context, target string) (cachedBrowserSession, error) {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return cachedBrowserSession{}, fmt.Errorf("parse cookie target: %w", err)
	}

	db, closeDB, err := c.open(ctx)
	if err != nil {
		return cachedBrowserSession{}, err
	}
	defer closeDB()

	_, _ = db.ExecContext(ctx, `DELETE FROM browser_cookies WHERE expires_at <= CURRENT_TIMESTAMP`)
	rows, err := db.QueryContext(ctx, `
		SELECT domain, path, name, value, expires_at, secure, http_only, user_agent
		FROM browser_cookies
		WHERE expires_at > CURRENT_TIMESTAMP
	`)
	if err != nil {
		return cachedBrowserSession{}, fmt.Errorf("load browser cookies: %w", err)
	}
	defer rows.Close()

	var session cachedBrowserSession
	host := strings.ToLower(u.Hostname())
	for rows.Next() {
		var domain, path, name, value, expiresAt, userAgent string
		var secure, httpOnly int
		if err := rows.Scan(&domain, &path, &name, &value, &expiresAt, &secure, &httpOnly, &userAgent); err != nil {
			return cachedBrowserSession{}, fmt.Errorf("scan browser cookie: %w", err)
		}
		if !domainMatches(host, domain) || !strings.HasPrefix(u.EscapedPath()+"/", strings.TrimSuffix(path, "/")+"/") {
			continue
		}
		expires, _ := time.Parse(time.RFC3339, expiresAt)
		session.cookies = append(session.cookies, &http.Cookie{
			Name:     name,
			Value:    value,
			Domain:   domain,
			Path:     path,
			Expires:  expires,
			Secure:   secure == 1,
			HttpOnly: httpOnly == 1,
		})
		if session.userAgent == "" {
			session.userAgent = userAgent
		}
	}
	if err := rows.Err(); err != nil {
		return cachedBrowserSession{}, fmt.Errorf("iterate browser cookies: %w", err)
	}
	return session, nil
}

func (c browserCookieCache) save(ctx context.Context, target, userAgent string, cookies []*http.Cookie) error {
	if len(cookies) == 0 {
		return nil
	}
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("parse cookie target: %w", err)
	}

	db, closeDB, err := c.open(ctx)
	if err != nil {
		return err
	}
	defer closeDB()

	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		domain := normalizeCookieDomain(cookie.Domain, u.Hostname())
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		expires := cookie.Expires
		if expires.IsZero() {
			expires = time.Now().Add(sessionCookieTTL)
		}
		if !expires.After(time.Now()) {
			continue
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO browser_cookies(domain, path, name, value, expires_at, secure, http_only, user_agent, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(domain, path, name) DO UPDATE SET
				value = excluded.value,
				expires_at = excluded.expires_at,
				secure = excluded.secure,
				http_only = excluded.http_only,
				user_agent = excluded.user_agent,
				updated_at = CURRENT_TIMESTAMP
		`, domain, path, cookie.Name, cookie.Value, expires.UTC().Format(time.RFC3339), database.BoolToInt(cookie.Secure), database.BoolToInt(cookie.HttpOnly), userAgent); err != nil {
			return fmt.Errorf("save browser cookie %s: %w", cookie.Name, err)
		}
	}
	return nil
}

// cookieMigrations tracks which cookie DB paths were migrated; load/save run
// once per FlareSolverr fetch and must not replay migrations every time.
var cookieMigrations sync.Map // dbPath -> *sync.Once

func (c browserCookieCache) open(ctx context.Context) (*sql.DB, func(), error) {
	db, err := database.Open(ctx, c.dbPath)
	if err != nil {
		return nil, nil, err
	}
	once, _ := cookieMigrations.LoadOrStore(c.dbPath, new(sync.Once))
	var migrateErr error
	once.(*sync.Once).Do(func() { migrateErr = database.Migrate(ctx, db) })
	if migrateErr != nil {
		cookieMigrations.Delete(c.dbPath)
		_ = db.Close()
		return nil, nil, migrateErr
	}
	return db, func() { _ = db.Close() }, nil
}

func normalizeCookieDomain(domain, fallbackHost string) string {
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" {
		domain = strings.ToLower(fallbackHost)
	}
	return domain
}

func domainMatches(host, domain string) bool {
	domain = normalizeCookieDomain(domain, "")
	if host == domain {
		return true
	}
	if net.ParseIP(host) != nil {
		return false
	}
	return strings.HasSuffix(host, "."+domain)
}
