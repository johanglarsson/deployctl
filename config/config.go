package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds all configuration for deployctl.
type Config struct {
	GitLabURL          string `mapstructure:"gitlab_url"`
	GitLabClientID     string `mapstructure:"gitlab_client_id"`
	GitLabRedirectPort int    `mapstructure:"gitlab_redirect_port"`
}

const defaultRedirectPort = 9876

// Load reads the config file at the given path (or the default location if
// path is empty) and returns a populated Config.
func Load(path string) (*Config, error) {
	v := viper.New()

	if path != "" {
		v.SetConfigFile(path)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cannot determine home directory: %w", err)
		}
		v.AddConfigPath(filepath.Join(home, ".deploycli"))
		v.SetConfigName("config")
		v.SetConfigType("yaml")
	}

	v.SetEnvPrefix("DEPLOYCLI")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		// A missing config file is not fatal — callers can still use env/flag auth.
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.GitLabRedirectPort == 0 {
		cfg.GitLabRedirectPort = defaultRedirectPort
	}

	return cfg, nil
}

// ValidateForOAuth returns an error if the config is missing fields required
// for the OAuth PKCE flow.
func (c *Config) ValidateForOAuth() error {
	if c.GitLabURL == "" {
		return errors.New("gitlab_url is required in config for OAuth login")
	}
	if len(c.GitLabURL) < 8 || c.GitLabURL[:8] != "https://" {
		return fmt.Errorf("gitlab_url must start with https://, got: %s", c.GitLabURL)
	}
	if c.GitLabClientID == "" {
		return errors.New("gitlab_client_id is required in config for OAuth login")
	}
	return nil
}
