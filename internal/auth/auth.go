// Package auth provides users, roles, permissions and cookie sessions.
package auth

import (
	"context"
	"time"
)

// Permission identifiers. Roles carry a set of these.
const (
	PermLibraryView    = "library.view"
	PermReaderUse      = "reader.use"
	PermLibraryManage  = "library.manage"
	PermSourcesManage  = "sources.manage"
	PermJobsManage     = "jobs.manage"
	PermSettingsManage = "settings.manage"
	PermUsersManage    = "users.manage"
)

// Permissions lists every known permission (stable order, for UIs).
func Permissions() []string {
	return []string{
		PermLibraryView, PermReaderUse, PermLibraryManage,
		PermSourcesManage, PermJobsManage, PermSettingsManage, PermUsersManage,
	}
}

// EnvAdminID is the fixed user id of the env-provisioned admin.
const EnvAdminID int64 = 1

const (
	OriginEnv     = "env"
	OriginLocal   = "local"
	OriginBuiltin = "builtin"
)

type User struct {
	ID        int64
	Username  string
	Origin    string // env|local
	RoleID    int64
	RoleName  string
	Perms     map[string]bool
	CreatedAt time.Time
}

// Can reports whether the user holds a permission.
func (u *User) Can(perm string) bool {
	if u == nil {
		return false
	}
	return u.Perms[perm]
}

// IsEnvAdmin reports whether this is the immutable env-provisioned admin.
func (u *User) IsEnvAdmin() bool { return u != nil && u.ID == EnvAdminID && u.Origin == OriginEnv }

type Role struct {
	ID     int64
	Name   string
	Origin string // builtin|local
	Perms  []string
}

// builtinRoles seeds the fixed role set; the admin role always holds every
// permission (kept in sync at boot even if the permission list grows).
func builtinRoles() []Role {
	return []Role{
		{Name: "admin", Origin: OriginBuiltin, Perms: Permissions()},
		{Name: "member", Origin: OriginBuiltin, Perms: []string{PermLibraryView, PermReaderUse, PermLibraryManage}},
		{Name: "viewer", Origin: OriginBuiltin, Perms: []string{PermLibraryView, PermReaderUse}},
	}
}

// --- request context plumbing ---

type ctxUserKey struct{}

// WithUser stores the authenticated user in a context.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxUserKey{}, u)
}

// FromContext returns the authenticated user, or nil.
func FromContext(ctx context.Context) *User {
	u, _ := ctx.Value(ctxUserKey{}).(*User)
	return u
}

// UserID returns the acting user's id; background jobs and CLI contexts that
// carry no user act as the env admin.
func UserID(ctx context.Context) int64 {
	if u := FromContext(ctx); u != nil {
		return u.ID
	}
	return EnvAdminID
}
