package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/johanglarsson/deployctl/internal/auth"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage GitLab authentication",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Authenticate with GitLab via OAuth PKCE",
	Long: `Authenticate with GitLab using the OAuth 2.0 PKCE flow.

Opens a browser window for GitLab login. The resulting token is stored
securely in the OS keychain and reused by subsequent commands.

If a valid cached token already exists, login is a no-op.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := GetConfig()

		// auth login always uses OAuth regardless of --ci / --agent flags
		oauthAuth := auth.NewOAuthForLogin(cfg)

		cached, entry, err := oauthAuth.HasCachedToken()
		if err != nil {
			return err
		}
		if cached {
			fmt.Fprintln(cmd.OutOrStdout(), "Already authenticated. Use 'deploycli auth status' to inspect the token.")
			return nil
		}
		if entry != nil && !entry.Expiry.IsZero() {
			fmt.Fprintf(cmd.ErrOrStderr(), "Cached token has expired, re-authenticating...\n")
		}

		token, err := oauthAuth.GetToken(cmd.Context())
		if err != nil {
			return fmt.Errorf("login failed: %w", err)
		}
		_ = token
		fmt.Fprintln(cmd.OutOrStdout(), "Successfully authenticated with GitLab.")
		return nil
	},
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove the cached GitLab token from the OS keychain",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := auth.DeleteStoredToken(); err != nil {
			return fmt.Errorf("logout failed: %w", err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Logged out. Token removed from keychain.")
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show current authentication status",
	Long: `Show whether a token is available and where it comes from.

In CI / agent mode the token source is reported (env var or --token flag).
In interactive mode the keychain is inspected.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mode := auth.DetectMode(flagToken, flagCI, flagAgent)
		out := cmd.OutOrStdout()

		if mode == auth.ModeAgent {
			// Token mode — report source without touching keychain
			ta := auth.NewTokenAuthForStatus(flagToken)
			src := ta.Source()
			token, err := ta.GetToken(cmd.Context())
			if err != nil {
				fmt.Fprintf(out, "Mode:   agent/CI\nToken:  not found (%v)\n", err)
				os.Exit(2)
			}
			fmt.Fprintf(out, "Mode:   agent/CI\nSource: %s\nToken:  %s...\n", src, maskToken(token))
			return nil
		}

		// Interactive mode — check keychain
		cfg := GetConfig()
		oauthAuth := auth.NewOAuthForLogin(cfg)
		cached, entry, err := oauthAuth.HasCachedToken()
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "Mode:   interactive (OAuth PKCE)\n")
		if !cached {
			if entry != nil && !entry.Expiry.IsZero() {
				fmt.Fprintf(out, "Token:  expired at %s\n", entry.Expiry.Format(time.RFC1123))
			} else {
				fmt.Fprintf(out, "Token:  not authenticated (run 'deploycli auth login')\n")
			}
			os.Exit(1)
		}
		expDesc := "no expiry"
		if !entry.Expiry.IsZero() {
			expDesc = fmt.Sprintf("expires %s", entry.Expiry.Format(time.RFC1123))
		}
		fmt.Fprintf(out, "Token:  %s... (%s)\n", maskToken(entry.Token), expDesc)
		return nil
	},
}

func init() {
	authCmd.AddCommand(authLoginCmd)
	authCmd.AddCommand(authLogoutCmd)
	authCmd.AddCommand(authStatusCmd)
	rootCmd.AddCommand(authCmd)
}

// maskToken returns the first 8 characters of a token followed by "****".
func maskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:8] + "****"
}
