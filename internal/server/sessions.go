package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type sessionRowView struct {
	Username string
	Device   string
	IP       string
	SignedIn string
	Online   bool
	LastSeen string
	Reading  string
}

func (u *webUI) sessionsFrag(w http.ResponseWriter, r *http.Request) {
	list, err := u.svc.Auth().ActiveSessions(r.Context())
	if err != nil {
		u.fail(w, err)
		return
	}
	rows := make([]sessionRowView, 0, len(list))
	for _, s := range list {
		row := sessionRowView{
			Username: s.Username,
			Device:   uaDevice(s.UserAgent),
			IP:       s.IP,
			SignedIn: relTime(s.CreatedAt),
			Online:   time.Since(s.LastSeenAt) < 5*time.Minute,
			LastSeen: relTime(s.LastSeenAt),
		}
		if a, ok := presence.Get(s.TokenHash, 3*time.Minute); ok {
			row.Reading = a.Title
			if a.ChapterLabel != "" {
				if row.Reading != "" {
					row.Reading += ": "
				}
				row.Reading += a.ChapterLabel
			}
			if a.Total > 0 {
				row.Reading += fmt.Sprintf(" · p %d/%d", a.Page, a.Total)
			}
		}
		rows = append(rows, row)
	}
	u.frag(w, "sessionsCard", rows)
}

// uaDevice reduces a user-agent string to "Browser · OS".
func uaDevice(ua string) string {
	if strings.TrimSpace(ua) == "" {
		return "Unknown device"
	}
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"):
		browser = "Opera"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	default:
		if first, _, ok := strings.Cut(ua, "/"); ok && len(first) < 20 {
			browser = first
		}
	}
	os := ""
	switch {
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		os = "iOS"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "Mac OS X"), strings.Contains(ua, "Macintosh"):
		os = "macOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	if os == "" {
		return browser
	}
	return browser + " · " + os
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
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
