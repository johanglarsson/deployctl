package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/johanglarsson/deployctl/config"
	"github.com/johanglarsson/deployctl/internal/auth"
	"github.com/spf13/cobra"
)

// contextKey is an unexported type for context keys in this package.
type contextKey int

const authKey contextKey = 0

var (
	// Global flag values
	flagToken      string
	flagCI         bool
	flagAgent      bool
	flagConfigPath string
	flagOutput     string
	flagEnv        string

	// Loaded at PersistentPreRunE and made available to subcommands
	globalConfig *config.Config
	globalAuth   auth.Authenticator
)

// rootCmd is the base command for the deploycli binary.
var rootCmd = &cobra.Command{
	Use:   "deploycli",
	Short: "A deployment CLI with GitLab authentication",
	Long: `deploycli is a deployment CLI that supports human-interactive use
via GitLab OAuth SSO and agent/CI automation via static tokens.`,
	SilenceUsage: true,
	// PersistentPreRunE runs before every subcommand, setting up config + auth.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip auth bootstrap for the auth subcommands themselves — they manage
		// auth directly.
		if cmd.Name() == "login" || cmd.Name() == "logout" || cmd.Name() == "status" {
			cfg, err := config.Load(flagConfigPath)
			if err != nil {
				return err
			}
			globalConfig = cfg
			return nil
		}

		cfg, err := config.Load(flagConfigPath)
		if err != nil {
			return err
		}
		globalConfig = cfg

		mode := auth.DetectMode(flagToken, flagCI, flagAgent)
		globalAuth = auth.New(cfg, mode, flagToken)
		return nil
	},
}

// Execute runs the root command. Called from main.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagToken, "token", "", "Auth token (agent/CI mode); prefer DEPLOYCLI_TOKEN env var")
	rootCmd.PersistentFlags().BoolVar(&flagCI, "ci", false, "Enable CI mode (auto-approve, structured output)")
	rootCmd.PersistentFlags().BoolVar(&flagAgent, "agent", false, "Enable agent mode (auto-approve, JSON output)")
	rootCmd.PersistentFlags().StringVar(&flagConfigPath, "config", "", "Path to config file (default: ~/.deploycli/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&flagOutput, "output", "text", "Output format: text | json")
	rootCmd.PersistentFlags().StringVar(&flagEnv, "env", "", "Target environment (e.g. staging, production)")
}

// GetAuthenticator returns the Authenticator built during PersistentPreRunE.
// Subcommands that need auth should call this.
func GetAuthenticator() auth.Authenticator {
	return globalAuth
}

// GetConfig returns the loaded Config.
func GetConfig() *config.Config {
	return globalConfig
}

// GetOutputFormat returns the value of the --output flag.
func GetOutputFormat() string {
	return flagOutput
}

// MustGetToken is a convenience helper that calls GetToken and exits on error.
func MustGetToken(ctx context.Context) string {
	token, err := globalAuth.GetToken(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
	return token
}
