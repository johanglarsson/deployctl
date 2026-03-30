package auth

import (
	"context"
	"os"

	"github.com/johanglarsson/deployctl/config"
)

// Authenticator is the common interface for all auth modes.
type Authenticator interface {
	// GetToken returns a valid access token, running interactive auth if needed.
	GetToken(ctx context.Context) (string, error)
	// Refresh forces a token refresh (re-runs OAuth flow or is a no-op for tokens).
	Refresh(ctx context.Context) error
	// Logout clears any cached credentials.
	Logout() error
}

// Mode describes which authentication strategy to use.
type Mode int

const (
	ModeInteractive Mode = iota // human user, OAuth PKCE
	ModeAgent                   // non-interactive, static token
)

// DetectMode decides which auth mode to use based on flags and environment.
//
// Priority (matching PLAN.md):
//  1. --ci flag or CI=true env → agent/CI mode
//  2. --agent flag or DEPLOYCLI_AGENT=true env → agent mode
//  3. DEPLOYCLI_TOKEN set and no TTY → agent mode
//  4. TTY present → interactive mode
func DetectMode(tokenFlag string, ciFlag, agentFlag bool) Mode {
	if ciFlag || os.Getenv("CI") == "true" {
		return ModeAgent
	}
	if agentFlag || os.Getenv("DEPLOYCLI_AGENT") == "true" {
		return ModeAgent
	}
	if (tokenFlag != "" || os.Getenv("DEPLOYCLI_TOKEN") != "") && !isTTY() {
		return ModeAgent
	}
	return ModeInteractive
}

// New constructs the appropriate Authenticator for the given mode.
// token is the value from the --token flag (may be empty).
func New(cfg *config.Config, mode Mode, token string) Authenticator {
	if mode == ModeAgent {
		return newTokenAuth(token)
	}
	return newOAuthAuth(cfg)
}

// isTTY reports whether stdin is an interactive terminal.
// Uses os.Stdin.Stat() to check for a character device — no external deps.
func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
