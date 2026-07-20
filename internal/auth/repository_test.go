package auth

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/brogergvhs/kaodoku/internal/database"
)

func TestMemberRoleHasNoJobsOrImportPerms(t *testing.T) {
	t.Parallel()

	for _, role := range builtinRoles() {
		if role.Name != "member" {
			continue
		}
		for _, p := range role.Perms {
			if p == PermJobsView || p == PermJobsManage || p == PermImportUse {
				t.Errorf("member role must not hold %s", p)
			}
		}
		return
	}
	t.Fatal("member role missing")
}

func TestUserContentGuardsRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "kaodoku.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	svc := NewService(db)
	if err := svc.Bootstrap(ctx, "admin", "secret"); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	roles, err := svc.ListRoles(ctx)
	if err != nil || len(roles) == 0 {
		t.Fatalf("ListRoles() = %v, %v", roles, err)
	}

	guards := ContentGuards{BlockedTags: []string{"Gore"}, AllowedTags: []string{"Kids"}}
	if err := svc.CreateUser(ctx, "kid", "pass1234", roles[0].ID, guards); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	var kid *User
	for i := range users {
		if users[i].Username == "kid" {
			kid = &users[i]
		}
	}
	if kid == nil {
		t.Fatal("created user not listed")
	}
	if !reflect.DeepEqual(kid.AllowedTags, []string{"Kids"}) || !reflect.DeepEqual(kid.BlockedTags, []string{"Gore"}) {
		t.Errorf("guards = allow %v block %v", kid.AllowedTags, kid.BlockedTags)
	}

	guards.AllowedTags = nil
	guards.AllowAdult = true
	if err := svc.UpdateUser(ctx, kid.ID, roles[0].ID, "", guards); err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	got, err := svc.GetUser(ctx, kid.ID)
	if err != nil || got == nil {
		t.Fatalf("GetUser() = %v, %v", got, err)
	}
	if len(got.AllowedTags) != 0 || !got.AllowAdult {
		t.Errorf("after update: allowed = %v, adult = %v", got.AllowedTags, got.AllowAdult)
	}
}

func TestPurgeExpiredSessions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := database.Open(ctx, filepath.Join(t.TempDir(), "kaodoku.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	svc := NewService(db)
	if err := svc.Bootstrap(ctx, "admin", "secret"); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ('old', 1, ?), ('fresh', 1, ?)`,
		database.FormatTime(time.Now().Add(-time.Hour)), database.FormatTime(time.Now().Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := svc.PurgeExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	var got []string
	rows, err := db.QueryContext(ctx, `SELECT token_hash FROM sessions ORDER BY token_hash`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatal(err)
		}
		got = append(got, hash)
	}
	if !reflect.DeepEqual(got, []string{"fresh"}) {
		t.Fatalf("sessions = %v, want fresh only", got)
	}
}
