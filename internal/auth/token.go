package auth

import (
	"context"
	"errors"
	"os"
)

// ErrNoToken is returned when no static token can be found from any source.
var ErrNoToken = errors.New("no auth token found: set DEPLOYCLI_TOKEN env var or use --token flag")

// TokenAuth implements Authenticator using a static token (agent/CI mode).
// Source priority: explicit token string → DEPLOYCLI_TOKEN env var.
type TokenAuth struct {
	token string
}

func newTokenAuth(tokenFlag string) *TokenAuth {
	return &TokenAuth{token: tokenFlag}
}

// NewTokenAuthForStatus creates a TokenAuth for use in auth status reporting.
func NewTokenAuthForStatus(tokenFlag string) *TokenAuth {
	return &TokenAuth{token: tokenFlag}
}

// GetToken returns the static token. It checks (in order):
//  1. The token provided at construction time (from --token flag)
//  2. The DEPLOYCLI_TOKEN environment variable
func (t *TokenAuth) GetToken(_ context.Context) (string, error) {
	if t.token != "" {
		return t.token, nil
	}
	if env := os.Getenv("DEPLOYCLI_TOKEN"); env != "" {
		return env, nil
	}
	return "", ErrNoToken
}

// Refresh is a no-op for static tokens.
func (t *TokenAuth) Refresh(_ context.Context) error { return nil }

// Logout is a no-op for static tokens.
func (t *TokenAuth) Logout() error { return nil }

// Source returns a human-readable description of where the token came from,
// useful for `auth status`.
func (t *TokenAuth) Source() string {
	if t.token != "" {
		return "--token flag"
	}
	if os.Getenv("DEPLOYCLI_TOKEN") != "" {
		return "DEPLOYCLI_TOKEN env var"
	}
	return "none"
}
