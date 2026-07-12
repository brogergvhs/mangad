package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// encPrefix marks encrypted values; stored strings without it are legacy
// plaintext and get re-encrypted on the next bootstrap.
const encPrefix = "enc:v1:"

// tokenCipher encrypts small secrets (AniList tokens) at rest with
// AES-256-GCM. The key comes from MANGAD_ENCRYPTION_KEY (64 hex chars) or an
// auto-generated mangad.key file next to the database.
type tokenCipher struct {
	aead cipher.AEAD
}

func newTokenCipher(dbPath string) (*tokenCipher, error) {
	key, err := loadOrCreateKey(dbPath)
	if err != nil {
		return nil, fmt.Errorf("encryption key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &tokenCipher{aead: aead}, nil
}

func loadOrCreateKey(dbPath string) ([]byte, error) {
	if env := strings.TrimSpace(os.Getenv("MANGAD_ENCRYPTION_KEY")); env != "" {
		key, err := hex.DecodeString(env)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("MANGAD_ENCRYPTION_KEY must be 64 hex characters")
		}
		return key, nil
	}
	path := filepath.Join(filepath.Dir(dbPath), "mangad.key")
	if data, err := os.ReadFile(path); err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("%s is corrupt; delete it to generate a new key (stored tokens will need reconnecting)", path)
		}
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)+"\n"), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func (c *tokenCipher) Encrypt(plain string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt returns stored values transparently: encrypted ones are opened,
// legacy plaintext passes through unchanged.
func (c *tokenCipher) Decrypt(stored string) (string, error) {
	if !strings.HasPrefix(stored, encPrefix) {
		return stored, nil
	}
	sealed, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(stored, encPrefix))
	if err != nil {
		return "", err
	}
	if len(sealed) < c.aead.NonceSize() {
		return "", fmt.Errorf("encrypted value too short")
	}
	plain, err := c.aead.Open(nil, sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt token (was the key file replaced?): %w", err)
	}
	return string(plain), nil
}
