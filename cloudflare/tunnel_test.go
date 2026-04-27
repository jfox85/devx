package cloudflare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfox85/devx/caddy"
)

func TestBuildCloudflaredConfig(t *testing.T) {
	sessions := map[string]*caddy.SessionInfo{
		"my-session": {
			Name:  "my-session",
			Ports: map[string]int{"ui": 3000, "api": 4000},
		},
	}

	cfg := buildCloudflaredConfig(sessions, "tunnel-abc", "/home/user/.cloudflared/creds.json", "example.com", 7777)

	if cfg.Tunnel != "tunnel-abc" {
		t.Errorf("expected tunnel=tunnel-abc, got %q", cfg.Tunnel)
	}
	if cfg.CredentialsFile != "/home/user/.cloudflared/creds.json" {
		t.Errorf("unexpected credentials-file %q", cfg.CredentialsFile)
	}
	if len(cfg.Ingress) == 0 {
		t.Fatal("expected at least one ingress rule")
	}

	// Check catch-all rule is last
	last := cfg.Ingress[len(cfg.Ingress)-1]
	if last.Hostname != "" || last.Service != "http_status:404" {
		t.Errorf("last rule must be catch-all, got %+v", last)
	}

	// Check service rules contain expected hostnames and services.
	var foundUI, foundAPI, foundWeb bool
	for _, rule := range cfg.Ingress {
		if rule.Hostname == "devx.example.com" {
			foundWeb = true
			if rule.Service != "http://localhost:7777" {
				t.Errorf("web service should proxy to devx web port, got %q", rule.Service)
			}
		}
		if rule.Hostname == "my-session-ui.example.com" {
			foundUI = true
			if !strings.Contains(rule.Service, "localhost") {
				t.Errorf("ui service should proxy to localhost, got %q", rule.Service)
			}
		}
		if rule.Hostname == "my-session-api.example.com" {
			foundAPI = true
		}
	}
	if !foundUI || !foundAPI || !foundWeb {
		t.Errorf("missing expected ingress rules: web=%v ui=%v api=%v", foundWeb, foundUI, foundAPI)
	}
}

func TestSyncTunnel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	sessions := map[string]*caddy.SessionInfo{
		"test-session": {
			Name:  "test-session",
			Ports: map[string]int{"ui": 3000},
		},
	}

	err := SyncTunnel(sessions, "tunnel-xyz", "/tmp/creds.json", "example.com", cfgPath, 7777)
	if err != nil {
		t.Fatalf("SyncTunnel returned error: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "tunnel-xyz") {
		t.Errorf("config missing tunnel ID: %s", content)
	}
	if !strings.Contains(content, "test-session-ui.example.com") {
		t.Errorf("config missing expected hostname: %s", content)
	}
	if !strings.Contains(content, "devx.example.com") {
		t.Errorf("config missing DevX Web PWA hostname: %s", content)
	}
	if !strings.Contains(content, "http_status:404") {
		t.Errorf("config missing catch-all rule: %s", content)
	}
}

func TestSyncTunnelSkipsWhenNotConfigured(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	sessions := map[string]*caddy.SessionInfo{}

	// empty domain — should be no-op
	err := SyncTunnel(sessions, "tid", "/tmp/creds.json", "", cfgPath, 7777)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Error("expected no file written when domain is empty")
	}

	// empty tunnelID — should be no-op
	err = SyncTunnel(sessions, "", "/tmp/creds.json", "example.com", cfgPath, 7777)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Error("expected no file written when tunnelID is empty")
	}
}
