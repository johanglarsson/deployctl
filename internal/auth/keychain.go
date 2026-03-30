package auth

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	keychainService = "deploycli"
	keychainUser    = "token"
	tokenFileName   = ".token"
)

// Entry is the in-memory representation of what is stored in the OS keychain.
// The raw value is formatted as "token|expiry_unix", where expiry_unix is 0
// when the token has no known expiry.
type Entry struct {
	Token  string
	Expiry time.Time // zero value means no expiry
}

// keychainEntry is an alias kept for internal use.
type keychainEntry = Entry

// DeleteStoredToken is a package-level convenience wrapper for DeleteEntry.
func DeleteStoredToken() error {
	return DeleteEntry()
}

// StoreEntry persists a token (and optional expiry).
// Tries the OS keychain first; falls back to ~/.deploycli/.token (mode 0600).
func StoreEntry(token string, expiry time.Time) error {
	var expiryUnix int64
	if !expiry.IsZero() {
		expiryUnix = expiry.Unix()
	}
	raw := fmt.Sprintf("%s|%d", token, expiryUnix)

	if err := keyring.Set(keychainService, keychainUser, raw); err == nil {
		return nil
	}
	// Keychain unavailable (e.g. headless Linux without Secret Service) —
	// fall back to a file with tight permissions.
	return storeFile(raw)
}

// LoadEntry retrieves the stored entry.
// Checks the OS keychain first, then the fallback file.
// Returns nil, nil when no entry exists in either location.
func LoadEntry() (*keychainEntry, error) {
	raw, err := keyring.Get(keychainService, keychainUser)
	if err == nil {
		return parseEntry(raw)
	}
	if !errors.Is(err, keyring.ErrNotFound) {
		// Keychain is broken/unavailable — try the file fallback silently.
		return loadFile()
	}
	// Keychain said "not found" — also check the file in case the user
	// switched environments.
	return loadFile()
}

// DeleteEntry removes the stored token from both the OS keychain and the
// fallback file.
func DeleteEntry() error {
	_ = deleteFile() // best-effort

	if err := keyring.Delete(keychainService, keychainUser); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("deleting token from keychain: %w", err)
	}
	return nil
}

// IsExpired reports whether the entry is expired (or about to expire within
// the given grace period).
func (e *keychainEntry) IsExpired(grace time.Duration) bool {
	if e.Expiry.IsZero() {
		return false
	}
	return time.Now().Add(grace).After(e.Expiry)
}

// --- file fallback ---

func tokenFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".deploycli", tokenFileName), nil
}

func storeFile(raw string) error {
	path, err := tokenFilePath()
	if err != nil {
		return fmt.Errorf("storing token file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating token directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		return fmt.Errorf("writing token file: %w", err)
	}
	return nil
}

func loadFile() (*keychainEntry, error) {
	path, err := tokenFilePath()
	if err != nil {
		return nil, nil //nolint:nilerr
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading token file: %w", err)
	}
	return parseEntry(strings.TrimSpace(string(data)))
}

func deleteFile() error {
	path, err := tokenFilePath()
	if err != nil {
		return nil //nolint:nilerr
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// --- shared parsing ---

func parseEntry(raw string) (*keychainEntry, error) {
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		// Legacy / plain token with no expiry.
		return &keychainEntry{Token: raw}, nil
	}
	token := parts[0]
	expiryUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing token expiry: %w", err)
	}
	entry := &keychainEntry{Token: token}
	if expiryUnix != 0 {
		entry.Expiry = time.Unix(expiryUnix, 0)
	}
	return entry, nil
}
