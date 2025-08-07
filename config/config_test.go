package config

import (
	"os"
	"testing"

	"github.com/spf13/viper"
)

func TestEnvOverride(t *testing.T) {
	// Reset viper for clean test
	viper.Reset()

	// Set default values
	viper.SetDefault("basedomain", "localhost")
	viper.SetDefault("caddy_api", "http://localhost:2019")
	viper.SetDefault("tmuxp_template", "~/.config/devx/session.yaml.tmpl")

	// Enable env vars
	viper.SetEnvPrefix("DEVX")
	viper.AutomaticEnv()

	// Set environment variable
	t.Setenv("DEVX_BASEDOMAIN", "foo.local")

	// Load config
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Check that environment variable overrides default
	if cfg.BaseDomain != "foo.local" {
		t.Fatalf("expected env override, got %s", cfg.BaseDomain)
	}

	// Check that other values remain as defaults
	if cfg.CaddyAPI != "http://localhost:2019" {
		t.Errorf("expected default caddy_api, got %s", cfg.CaddyAPI)
	}
}

func TestTildeExpansion(t *testing.T) {
	// Reset viper for clean test
	viper.Reset()

	// Set value with tilde
	viper.Set("tmuxp_template", "~/test/path.yaml")
	viper.Set("basedomain", "localhost")
	viper.Set("caddy_api", "http://localhost:2019")

	// Load config
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Get home directory
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get home directory: %v", err)
	}

	// Check that tilde was expanded
	expected := home + "/test/path.yaml"
	if cfg.TmuxpTemplate != expected {
		t.Errorf("expected tilde expansion to %s, got %s", expected, cfg.TmuxpTemplate)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	// Reset viper for clean test
	viper.Reset()

	// Set defaults
	viper.SetDefault("basedomain", "localhost")
	viper.SetDefault("caddy_api", "http://localhost:2019")
	viper.SetDefault("tmuxp_template", "~/.config/devx/session.yaml.tmpl")

	// Load config
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	// Check defaults
	if cfg.BaseDomain != "localhost" {
		t.Errorf("expected default basedomain 'localhost', got %s", cfg.BaseDomain)
	}

	if cfg.CaddyAPI != "http://localhost:2019" {
		t.Errorf("expected default caddy_api 'http://localhost:2019', got %s", cfg.CaddyAPI)
	}
}
