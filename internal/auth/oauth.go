package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/johanglarsson/deployctl/config"
)

const (
	// tokenExpiryGrace is how close to expiry we treat a token as expired.
	tokenExpiryGrace = 60 * time.Second
)

// OAuthAuth implements Authenticator using GitLab OAuth PKCE flow.
type OAuthAuth struct {
	cfg *config.Config
}

func newOAuthAuth(cfg *config.Config) *OAuthAuth {
	return &OAuthAuth{cfg: cfg}
}

// NewOAuthForLogin creates an OAuthAuth for use in auth login/status commands.
func NewOAuthForLogin(cfg *config.Config) *OAuthAuth {
	return &OAuthAuth{cfg: cfg}
}

// GetToken returns a valid token from the keychain, running the PKCE flow if
// no valid cached token exists.
func (o *OAuthAuth) GetToken(ctx context.Context) (string, error) {
	entry, err := LoadEntry()
	if err != nil {
		return "", err
	}
	if entry != nil && !entry.IsExpired(tokenExpiryGrace) {
		return entry.Token, nil
	}
	return o.runPKCEFlow(ctx)
}

// Refresh forces a new PKCE flow regardless of any cached token.
func (o *OAuthAuth) Refresh(ctx context.Context) error {
	_, err := o.runPKCEFlow(ctx)
	return err
}

// Logout removes the cached token from the OS keychain.
func (o *OAuthAuth) Logout() error {
	return DeleteEntry()
}

// HasCachedToken reports whether a non-expired token exists in the keychain.
func (o *OAuthAuth) HasCachedToken() (bool, *Entry, error) {
	entry, err := LoadEntry()
	if err != nil {
		return false, nil, err
	}
	if entry == nil || entry.IsExpired(tokenExpiryGrace) {
		return false, entry, nil
	}
	return true, entry, nil
}

// runPKCEFlow executes the full GitLab OAuth 2.0 PKCE authorization flow.
func (o *OAuthAuth) runPKCEFlow(ctx context.Context) (string, error) {
	if err := o.cfg.ValidateForOAuth(); err != nil {
		return "", err
	}

	// 1. Generate PKCE verifier + challenge
	verifier, challenge, err := generatePKCE()
	if err != nil {
		return "", fmt.Errorf("generating PKCE: %w", err)
	}

	// 2. Generate random state to prevent CSRF
	state, err := randomHex(16)
	if err != nil {
		return "", fmt.Errorf("generating state: %w", err)
	}

	// 3. Start local callback server on the configured port
	port := o.cfg.GitLabRedirectPort
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", port)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return "", fmt.Errorf("starting callback server on port %d: %w", port, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			errCh <- fmt.Errorf("OAuth state mismatch — possible CSRF")
			return
		}
		if errParam := q.Get("error"); errParam != "" {
			desc := q.Get("error_description")
			http.Error(w, "auth error", http.StatusBadRequest)
			errCh <- fmt.Errorf("GitLab OAuth error: %s — %s", errParam, desc)
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- fmt.Errorf("no authorization code in callback")
			return
		}
		fmt.Fprintln(w, "<html><body><h2>Authentication successful. You can close this tab.</h2></body></html>")
		codeCh <- code
	})

	srv := &http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("callback server error: %w", err)
		}
	}()
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	// 4. Build authorize URL and open browser
	authURL := o.buildAuthorizeURL(redirectURI, challenge, state)
	fmt.Fprintf(os.Stderr, "Opening browser for GitLab login...\n%s\n\n", authURL)
	openBrowser(authURL)

	// 5. Wait for the authorization code (or timeout / context cancel)
	select {
	case code := <-codeCh:
		return o.exchangeCode(ctx, code, verifier, redirectURI)
	case err := <-errCh:
		return "", err
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("timed out waiting for OAuth callback (5 min)")
	}
}

// buildAuthorizeURL constructs the GitLab OAuth authorization URL.
func (o *OAuthAuth) buildAuthorizeURL(redirectURI, challenge, state string) string {
	params := url.Values{
		"client_id":             {o.cfg.GitLabClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {o.cfg.GitLabScope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return fmt.Sprintf("%s/oauth/authorize?%s", strings.TrimRight(o.cfg.GitLabURL, "/"), params.Encode())
}

// tokenResponse is the JSON shape returned by GitLab's token endpoint.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"` // seconds; 0 means no expiry
	Scope       string `json:"scope"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

// exchangeCode trades the authorization code for an access token.
func (o *OAuthAuth) exchangeCode(ctx context.Context, code, verifier, redirectURI string) (string, error) {
	tokenURL := fmt.Sprintf("%s/oauth/token", strings.TrimRight(o.cfg.GitLabURL, "/"))

	body := url.Values{
		"client_id":     {o.cfg.GitLabClientID},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(body.Encode()))
	if err != nil {
		return "", fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decoding token response: %w", err)
	}
	if tr.Error != "" {
		return "", fmt.Errorf("token exchange failed: %s — %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token exchange returned empty access_token (HTTP %d)", resp.StatusCode)
	}

	// Compute expiry and persist to keychain
	var expiry time.Time
	if tr.ExpiresIn > 0 {
		expiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	if err := StoreEntry(tr.AccessToken, expiry); err != nil {
		// Not fatal — warn but return the token so the caller can proceed
		fmt.Fprintf(os.Stderr, "warning: could not store token in keychain: %v\n", err)
	}

	return tr.AccessToken, nil
}

// generatePKCE creates a PKCE code_verifier (32 random bytes, base64url-encoded)
// and the corresponding S256 code_challenge.
func generatePKCE() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

// randomHex returns a hex string of n random bytes.
func randomHex(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", raw), nil
}

// openBrowser attempts to open the given URL in the default browser.
// Falls back silently if no browser is available (e.g. headless environments).
func openBrowser(u string) {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "linux":
		cmd = "xdg-open"
		args = []string{u}
	case "darwin":
		cmd = "open"
		args = []string{u}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", u}
	default:
		return
	}

	if err := exec.Command(cmd, args...).Start(); err != nil {
		// Headless / no browser — URL has already been printed to stderr
		_ = err
	}
}
