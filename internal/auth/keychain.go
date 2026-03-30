package auth

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	keychainService = "deploycli"
	keychainUser    = "token"
)

// Entry is the in-memory representation of what is stored in the OS keychain.
// The raw keychain value is formatted as "token|expiry_unix", where expiry_unix
// is 0 when the token has no known expiry.
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

// StoreEntry persists a token (and optional expiry) in the OS keychain.
func StoreEntry(token string, expiry time.Time) error {
	var expiryUnix int64
	if !expiry.IsZero() {
		expiryUnix = expiry.Unix()
	}
	raw := fmt.Sprintf("%s|%d", token, expiryUnix)
	if err := keyring.Set(keychainService, keychainUser, raw); err != nil {
		return fmt.Errorf("storing token in keychain: %w", err)
	}
	return nil
}

// LoadEntry retrieves the stored entry from the OS keychain.
// Returns nil, nil when no entry exists.
func LoadEntry() (*keychainEntry, error) {
	raw, err := keyring.Get(keychainService, keychainUser)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("loading token from keychain: %w", err)
	}
	return parseEntry(raw)
}

// DeleteEntry removes the stored token from the OS keychain.
func DeleteEntry() error {
	if err := keyring.Delete(keychainService, keychainUser); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil // already gone
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

func parseEntry(raw string) (*keychainEntry, error) {
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		// Legacy: plain token with no expiry
		return &keychainEntry{Token: raw}, nil
	}
	token := parts[0]
	expiryUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parsing keychain entry expiry: %w", err)
	}
	entry := &keychainEntry{Token: token}
	if expiryUnix != 0 {
		entry.Expiry = time.Unix(expiryUnix, 0)
	}
	return entry, nil
}
