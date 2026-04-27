package cloudflare

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jfox85/devx/caddy"
)

func TestCheckTunnelConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	sessions := map[string]*caddy.SessionInfo{
		"sess": {Name: "sess", Ports: map[string]int{"ui": 3000}},
	}

	// Write a valid config
	if err := SyncTunnel(sessions, "tid", "/tmp/c.json", "ex.com", cfgPath, 7777); err != nil {
		t.Fatalf("SyncTunnel: %v", err)
	}

	result := CheckTunnel(sessions, "tid", "ex.com", cfgPath, 7777)

	if !result.ConfigExists {
		t.Error("expected ConfigExists=true")
	}
	if !result.ConfigValid {
		t.Errorf("expected ConfigValid=true, err: %s", result.ConfigError)
	}
	if result.IngressMismatch {
		t.Error("expected no ingress mismatch after fresh sync")
	}

	// Test with missing config file
	result2 := CheckTunnel(sessions, "tid", "ex.com", "/nonexistent/config.yaml", 7777)
	if result2.ConfigExists {
		t.Error("expected ConfigExists=false for missing file")
	}
}

func TestCheckTunnelConfigMismatch(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Sync with one set of sessions
	originalSessions := map[string]*caddy.SessionInfo{
		"sess-a": {Name: "sess-a", Ports: map[string]int{"ui": 3000}},
	}
	if err := SyncTunnel(originalSessions, "tid", "/tmp/c.json", "ex.com", cfgPath, 7777); err != nil {
		t.Fatalf("SyncTunnel: %v", err)
	}

	// Check with a different (larger) set of sessions — new session not in config
	newSessions := map[string]*caddy.SessionInfo{
		"sess-a": {Name: "sess-a", Ports: map[string]int{"ui": 3000}},
		"sess-b": {Name: "sess-b", Ports: map[string]int{"api": 4000}},
	}
	result := CheckTunnel(newSessions, "tid", "ex.com", cfgPath, 7777)

	if !result.ConfigExists {
		t.Error("expected ConfigExists=true")
	}
	if !result.ConfigValid {
		t.Errorf("expected ConfigValid=true, err: %s", result.ConfigError)
	}
	if !result.IngressMismatch {
		t.Error("expected IngressMismatch=true when config is out of date")
	}
	if len(result.MissingRules) == 0 {
		t.Error("expected at least one missing rule")
	}
}

func TestCheckTunnelInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Write invalid YAML
	if err := os.WriteFile(cfgPath, []byte("::invalid yaml::\n\t bad"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sessions := map[string]*caddy.SessionInfo{}
	result := CheckTunnel(sessions, "tid", "ex.com", cfgPath, 7777)

	if !result.ConfigExists {
		t.Error("expected ConfigExists=true")
	}
	if result.ConfigValid {
		t.Error("expected ConfigValid=false for invalid YAML")
	}
	if result.ConfigError == "" {
		t.Error("expected ConfigError to be set")
	}
}
