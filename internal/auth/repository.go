package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/brogergvhs/mangad/internal/database"
)

const sessionTTL = 30 * 24 * time.Hour

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Service manages users, roles and sessions.
type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service { return &Service{db: db} }

// Bootstrap seeds built-in roles and the env admin (user id 1). It runs at
// every startup so the admin credentials always mirror the environment and
// the admin role always covers newly added permissions.
func (s *Service) Bootstrap(ctx context.Context, adminUser, adminPassword string) error {
	for _, role := range builtinRoles() {
		perms, _ := json.Marshal(role.Perms)
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO roles (name, origin, permissions_json) VALUES (?, 'builtin', ?)
			ON CONFLICT(name) DO UPDATE SET permissions_json = excluded.permissions_json
			WHERE roles.origin = 'builtin'
		`, role.Name, string(perms)); err != nil {
			return fmt.Errorf("seed role %s: %w", role.Name, err)
		}
	}
	adminUser = strings.TrimSpace(adminUser)
	if adminUser == "" {
		adminUser = "admin"
	}
	hash := ""
	if adminPassword != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
		if err != nil {
			return fmt.Errorf("hash admin password: %w", err)
		}
		hash = string(h)
	}
	var adminRole int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM roles WHERE name = 'admin'`).Scan(&adminRole); err != nil {
		return fmt.Errorf("load admin role: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, role_id, origin, allow_adult)
		VALUES (?, ?, ?, ?, 'env', 1)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			password_hash = excluded.password_hash,
			role_id = excluded.role_id,
			origin = 'env',
			allow_adult = 1
	`, EnvAdminID, adminUser, hash, adminRole); err != nil {
		return fmt.Errorf("bootstrap env admin: %w", err)
	}
	return nil
}

// Login verifies credentials and returns a new session token.
func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	var id int64
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT id, password_hash FROM users WHERE username = ?`, strings.TrimSpace(username)).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && hash == "") {
		return "", fmt.Errorf("invalid credentials")
	}
	if err != nil {
		return "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", fmt.Errorf("invalid credentials")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	expires := time.Now().Add(sessionTTL)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)
	`, hex.EncodeToString(sum[:]), id, database.FormatTime(expires)); err != nil {
		return "", fmt.Errorf("store session: %w", err)
	}
	return token, nil
}

// UserBySession resolves a session token to its user, or nil when invalid.
func (s *Service) UserBySession(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(token))
	var id int64
	var expires string
	err := s.db.QueryRowContext(ctx, `SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`,
		hex.EncodeToString(sum[:])).Scan(&id, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t, err := database.ParseTime(expires); err != nil || time.Now().After(t) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hex.EncodeToString(sum[:]))
		return nil, nil
	}
	return s.GetUser(ctx, id)
}

// Logout deletes the session for a token.
func (s *Service) Logout(ctx context.Context, token string) {
	sum := sha256.Sum256([]byte(token))
	_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hex.EncodeToString(sum[:]))
}

// GetUser loads one user with its role and permissions.
func (s *Service) GetUser(ctx context.Context, id int64) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.origin, u.allow_adult, u.created_at, r.id, r.name, r.permissions_json
		FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = ?`, id)
	return scanUser(row)
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	var created, perms string
	var allowAdult int
	if err := row.Scan(&u.ID, &u.Username, &u.Origin, &allowAdult, &created, &u.RoleID, &u.RoleName, &perms); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	u.AllowAdult = allowAdult != 0
	u.CreatedAt, _ = database.ParseTime(created)
	var list []string
	_ = json.Unmarshal([]byte(perms), &list)
	u.Perms = make(map[string]bool, len(list))
	for _, p := range list {
		u.Perms[p] = true
	}
	return &u, nil
}

// ListUsers returns all users with role names.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.username, u.origin, u.allow_adult, u.created_at, r.id, r.name, r.permissions_json
		FROM users u JOIN roles r ON r.id = u.role_id ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		var created, perms string
		var allowAdult int
		if err := rows.Scan(&u.ID, &u.Username, &u.Origin, &allowAdult, &created, &u.RoleID, &u.RoleName, &perms); err != nil {
			return nil, err
		}
		u.AllowAdult = allowAdult != 0
		u.CreatedAt, _ = database.ParseTime(created)
		var list []string
		_ = json.Unmarshal([]byte(perms), &list)
		u.Perms = make(map[string]bool, len(list))
		for _, p := range list {
			u.Perms[p] = true
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateUser adds a local user.
func (s *Service) CreateUser(ctx context.Context, username, password string, roleID int64, allowAdult bool) error {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 4 {
		return fmt.Errorf("username and a password of at least 4 characters are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role_id, origin, allow_adult) VALUES (?, ?, ?, 'local', ?)
	`, username, string(hash), roleID, boolInt(allowAdult)); err != nil {
		return fmt.Errorf("create user %q: %w", username, err)
	}
	return nil
}

// UpdateUser changes a local user's role and optionally password. The env
// admin is immutable.
func (s *Service) UpdateUser(ctx context.Context, id int64, roleID int64, password string, allowAdult bool) error {
	if id == EnvAdminID {
		return fmt.Errorf("the environment admin cannot be edited")
	}
	if password != "" {
		if len(password) < 4 {
			return fmt.Errorf("password must be at least 4 characters")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `UPDATE users SET role_id = ?, allow_adult = ?, password_hash = ? WHERE id = ? AND origin != 'env'`, roleID, boolInt(allowAdult), string(hash), id)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET role_id = ?, allow_adult = ? WHERE id = ? AND origin != 'env'`, roleID, boolInt(allowAdult), id)
	return err
}

// DeleteUser removes a local user (never the env admin) and their sessions.
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	if id == EnvAdminID {
		return fmt.Errorf("the environment admin cannot be deleted")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ? AND origin != 'env'`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
	return err
}

// ListRoles returns all roles.
func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, origin, permissions_json FROM roles ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Role
	for rows.Next() {
		var r Role
		var perms string
		if err := rows.Scan(&r.ID, &r.Name, &r.Origin, &perms); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(perms), &r.Perms)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveRole creates or updates a custom role. Built-in roles are immutable.
func (s *Service) SaveRole(ctx context.Context, id int64, name string, perms []string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("role name is required")
	}
	valid := map[string]bool{}
	for _, p := range Permissions() {
		valid[p] = true
	}
	clean := perms[:0]
	for _, p := range perms {
		if valid[p] {
			clean = append(clean, p)
		}
	}
	data, _ := json.Marshal(clean)
	if id == 0 {
		_, err := s.db.ExecContext(ctx, `INSERT INTO roles (name, origin, permissions_json) VALUES (?, 'local', ?)`, name, string(data))
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE roles SET name = ?, permissions_json = ? WHERE id = ? AND origin = 'local'`, name, string(data), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("built-in roles cannot be edited")
	}
	return nil
}

// DeleteRole removes an unused custom role.
func (s *Service) DeleteRole(ctx context.Context, id int64) error {
	var users int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role_id = ?`, id).Scan(&users); err != nil {
		return err
	}
	if users > 0 {
		return fmt.Errorf("role is still assigned to %d user(s)", users)
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM roles WHERE id = ? AND origin = 'local'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("built-in roles cannot be deleted")
	}
	return nil
}

// ChangePassword lets a local user rotate their own password after verifying
// the current one. The env admin's password lives in the environment.
func (s *Service) ChangePassword(ctx context.Context, userID int64, current, next string) error {
	if userID == EnvAdminID {
		return fmt.Errorf("the environment admin's password is managed via MANGAD_ADMIN_PASSWORD")
	}
	if len(next) < 4 {
		return fmt.Errorf("the new password must be at least 4 characters")
	}
	var hash string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)) != nil {
		return fmt.Errorf("current password is incorrect")
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, string(newHash), userID)
	return err
}

// SessionInfo describes one active session.
type SessionInfo struct {
	CreatedAt string
	ExpiresAt string
	Current   bool
}

// Sessions lists the user's active sessions; currentToken marks the caller's.
func (s *Service) Sessions(ctx context.Context, userID int64, currentToken string) ([]SessionInfo, error) {
	sum := sha256.Sum256([]byte(currentToken))
	cur := hex.EncodeToString(sum[:])
	rows, err := s.db.QueryContext(ctx, `SELECT token_hash, created_at, expires_at FROM sessions WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var hash, created, expires string
		if rows.Scan(&hash, &created, &expires) != nil {
			continue
		}
		out = append(out, SessionInfo{CreatedAt: created, ExpiresAt: expires, Current: hash == cur})
	}
	return out, rows.Err()
}

// RevokeOtherSessions signs the user out everywhere except the current session.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID int64, currentToken string) error {
	sum := sha256.Sum256([]byte(currentToken))
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, userID, hex.EncodeToString(sum[:]))
	return err
}

// APIToken is a named header token for scripted clients.
type APIToken struct {
	ID        int64
	Name      string
	CreatedAt string
}

// CreateAPIToken mints a token for a user; the raw value is shown once.
func (s *Service) CreateAPIToken(ctx context.Context, userID int64, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "token"
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := "mgd_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	if _, err := s.db.ExecContext(ctx, `INSERT INTO api_tokens (token_hash, user_id, name) VALUES (?, ?, ?)`,
		hex.EncodeToString(sum[:]), userID, name); err != nil {
		return "", err
	}
	return token, nil
}

// APITokens lists a user's tokens (hashes never leave the DB).
func (s *Service) APITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rowid, name, created_at FROM api_tokens WHERE user_id = ? ORDER BY rowid`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if rows.Scan(&t.ID, &t.Name, &t.CreatedAt) == nil {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// DeleteAPIToken revokes one of the user's tokens.
func (s *Service) DeleteAPIToken(ctx context.Context, userID, tokenID int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE rowid = ? AND user_id = ?`, tokenID, userID)
	return err
}

// UserByAPIToken resolves a header token to its user, or nil.
func (s *Service) UserByAPIToken(ctx context.Context, token string) (*User, error) {
	if !strings.HasPrefix(token, "mgd_") {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(token))
	var userID int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM api_tokens WHERE token_hash = ?`, hex.EncodeToString(sum[:])).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetUser(ctx, userID)
}
