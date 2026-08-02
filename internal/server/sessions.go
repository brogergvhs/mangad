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
		rows = append(rows, sessionRowView{
			Username: s.Username,
			Device:   uaDevice(s.UserAgent),
			IP:       s.IP,
			SignedIn: relTime(s.CreatedAt),
			Online:   time.Since(s.LastSeenAt) < 5*time.Minute,
			LastSeen: relTime(s.LastSeenAt),
			Reading:  readingLabel(s.TokenHash),
		})
	}
	if devices, err := u.svc.Auth().ActiveDevices(r.Context()); err == nil {
		for _, d := range devices {
			rows = append(rows, sessionRowView{
				Username: d.Username,
				Device:   d.Name,
				IP:       "—",
				SignedIn: relTime(d.CreatedAt),
				Online:   time.Since(d.LastSeenAt) < 5*time.Minute,
				LastSeen: relTime(d.LastSeenAt),
				Reading:  readingLabel(d.TokenHash),
			})
		}
	}
	u.frag(w, "sessionsCard", rows)
}

// readingLabel formats a session's reader presence ("Title: Ch 5 · p 3/40").
func readingLabel(tokenHash string) string {
	a, ok := presence.Get(tokenHash, 3*time.Minute)
	if !ok {
		return ""
	}
	out := a.Title
	if a.ChapterLabel != "" {
		if out != "" {
			out += ": "
		}
		out += a.ChapterLabel
	}
	if a.Total > 0 {
		out += fmt.Sprintf(" · p %d/%d", a.Page, a.Total)
	}
	return out
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
