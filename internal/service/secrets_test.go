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
