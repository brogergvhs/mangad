package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brogergvhs/kaodoku/internal/catalog"
)

func TestTokenCipherRoundTripAndLegacy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c, err := newTokenCipher(filepath.Join(dir, "kaodoku.db"))
	if err != nil {
		t.Fatal(err)
	}
	enc, err := c.Encrypt("secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, encPrefix) || strings.Contains(enc, "secret-token") {
		t.Fatalf("ciphertext looks wrong: %q", enc)
	}
	got, err := c.Decrypt(enc)
	if err != nil || got != "secret-token" {
		t.Fatalf("Decrypt = %q, %v", got, err)
	}
	// Legacy plaintext passes through.
	if got, err := c.Decrypt("plain-old-token"); err != nil || got != "plain-old-token" {
		t.Fatalf("legacy Decrypt = %q, %v", got, err)
	}
	// A second cipher from the same key file must open the first's output.
	c2, err := newTokenCipher(filepath.Join(dir, "kaodoku.db"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c2.Decrypt(enc); err != nil || got != "secret-token" {
		t.Fatalf("cross-instance Decrypt = %q, %v", got, err)
	}
}

// Plaintext rows left from before encryption get re-encrypted at bootstrap and
// still round-trip through the identity lookup.
func TestEncryptLegacyTokens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "kaodoku.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()

	if _, err := svc.db.ExecContext(ctx, `INSERT INTO user_anilist (user_id, access_token, anilist_user_id, anilist_name) VALUES (1, 'legacy-plain', 7, 'x')`); err != nil {
		t.Fatal(err)
	}
	if err := svc.encryptLegacyTokens(ctx); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := svc.db.QueryRowContext(ctx, `SELECT access_token FROM user_anilist WHERE user_id = 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, encPrefix) {
		t.Fatalf("token not encrypted: %q", stored)
	}
	tctx, aid, ok := svc.aniListIdentity(ctx, 1)
	if !ok || aid != 7 {
		t.Fatalf("identity = (%d, %v)", aid, ok)
	}
	if tok := catalog.TokenFromContext(tctx); tok != "legacy-plain" {
		t.Fatalf("decrypted token = %q", tok)
	}
}

func TestRotateEncryptionKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "kaodoku.db")

	svc, closeDB, err := OpenJobs(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Store an encrypted token, then release the DB before rotating (rotation
	// opens the DB itself, as the CLI command does with the server stopped).
	enc, err := svc.secrets.Encrypt("anilist-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.ExecContext(ctx, `INSERT INTO user_anilist (user_id, access_token, anilist_user_id, anilist_name) VALUES (1, ?, 7, 'x')`, enc); err != nil {
		t.Fatal(err)
	}
	closeDB()

	n, err := RotateEncryptionKey(ctx, dbPath)
	if err != nil {
		t.Fatalf("RotateEncryptionKey() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("rotated %d secrets, want 1", n)
	}

	// A cipher from the new key file decrypts the re-encrypted value; the stored
	// ciphertext must have changed (new key + new nonce).
	newCipher, err := newTokenCipher(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	svc2, closeDB2, err := OpenJobs(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB2()
	var stored string
	if err := svc2.db.QueryRowContext(ctx, `SELECT access_token FROM user_anilist WHERE user_id = 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == enc {
		t.Fatal("ciphertext unchanged after rotation")
	}
	got, err := newCipher.Decrypt(stored)
	if err != nil || got != "anilist-token" {
		t.Fatalf("decrypt with rotated key = %q, %v", got, err)
	}
}

func TestRotateEncryptionKeyRefusesEnvKey(t *testing.T) {
	t.Setenv("KAODOKU_ENCRYPTION_KEY", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	if _, err := RotateEncryptionKey(context.Background(), filepath.Join(t.TempDir(), "kaodoku.db")); err == nil {
		t.Fatal("expected refusal when key is pinned via env")
	}
}

func TestNotifications(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	svc, closeDB, err := OpenJobs(ctx, filepath.Join(t.TempDir(), "kaodoku.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()

	server := NotificationScope{UserID: 1, Server: true}
	own := NotificationScope{UserID: 2}
	if err := svc.AddNotification(ctx, 0, "error", "boom", 42); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddNotification(ctx, 2, "info", "yours", 0); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.UnreadNotificationCount(ctx, server); c != 1 {
		t.Fatalf("server unread = %d, want 1", c)
	}
	if c, _ := svc.UnreadNotificationCount(ctx, own); c != 1 {
		t.Fatalf("own unread = %d, want 1", c)
	}
	if c, _ := svc.UnreadNotificationCount(ctx, NotificationScope{All: true}); c != 2 {
		t.Fatalf("all unread = %d, want 2", c)
	}
	if err := svc.MarkNotificationsRead(ctx, server); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.UnreadNotificationCount(ctx, server); c != 0 {
		t.Fatalf("server unread after mark = %d, want 0", c)
	}
	if c, _ := svc.UnreadNotificationCount(ctx, own); c != 1 {
		t.Fatalf("own unread after server mark = %d, want 1", c)
	}

	// Insert past the cap; only the most recent maxNotifications survive.
	for i := 0; i < maxNotifications+5; i++ {
		if err := svc.AddNotification(ctx, 0, "error", "n", 0); err != nil {
			t.Fatal(err)
		}
	}
	items, err := svc.Notifications(ctx, NotificationScope{All: true}, maxNotifications+50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != maxNotifications {
		t.Fatalf("stored %d notifications, want cap %d", len(items), maxNotifications)
	}
}
