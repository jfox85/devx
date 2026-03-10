package cloudflare

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jfox85/devx/caddy"
	"gopkg.in/yaml.v3"
)

// expandPath expands a leading ~/ to the user's home directory.
func expandPath(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// CloudflaredConfig represents the full cloudflared YAML config
type CloudflaredConfig struct {
	Tunnel          string        `yaml:"tunnel"`
	CredentialsFile string        `yaml:"credentials-file"`
	Ingress         []IngressRule `yaml:"ingress"`
}

// IngressRule represents one cloudflared ingress rule
type IngressRule struct {
	Hostname      string         `yaml:"hostname,omitempty"`
	Service       string         `yaml:"service"`
	OriginRequest *OriginRequest `yaml:"originRequest,omitempty"`
}

// OriginRequest holds per-rule origin connection options.
type OriginRequest struct {
	HTTPHostHeader string `yaml:"httpHostHeader,omitempty"`
}

// buildCloudflaredConfig generates the cloudflared config from current sessions.
func buildCloudflaredConfig(sessions map[string]*caddy.SessionInfo, tunnelID, credentialsFile, domain string) CloudflaredConfig {
	var rules []IngressRule

	// Sort session names for deterministic output
	names := make([]string, 0, len(sessions))
	for name := range sessions {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, sessionName := range names {
		info := sessions[sessionName]

		// Sort service names for deterministic output
		services := make([]string, 0, len(info.Ports))
		for svc := range info.Ports {
			services = append(services, svc)
		}
		sort.Strings(services)

		for _, serviceName := range services {
			externalHost := caddy.BuildExternalHostname(sessionName, serviceName, info.ProjectAlias, domain)
			if externalHost == "" {
				continue
			}
			port, ok := info.Ports[serviceName]
			if !ok || port == 0 {
				continue
			}
			// Connect directly to the service port, bypassing Caddy's reverse
			// proxy. This avoids transfer-encoding mismatches in the proxy chain
			// and ensures cloudflared speaks directly to the origin.
			rules = append(rules, IngressRule{
				Hostname: externalHost,
				Service:  fmt.Sprintf("http://localhost:%d", port),
				// Send Host: localhost so dev servers (Vite, webpack-dev-server, etc.)
				// don't reject the request as an unrecognised external hostname.
				OriginRequest: &OriginRequest{HTTPHostHeader: "localhost"},
			})
		}
	}

	// Catch-all rule required by cloudflared
	rules = append(rules, IngressRule{Service: "http_status:404"})

	return CloudflaredConfig{
		Tunnel:          tunnelID,
		CredentialsFile: credentialsFile,
		Ingress:         rules,
	}
}

// SyncTunnel generates the cloudflared config file from current sessions.
// Skips if domain or tunnelID is empty.
func SyncTunnel(sessions map[string]*caddy.SessionInfo, tunnelID, credentialsFile, domain, cfgPath string) error {
	if domain == "" || tunnelID == "" {
		return nil
	}

	cfgPath = expandPath(cfgPath)
	credentialsFile = expandPath(credentialsFile)

	cfg := buildCloudflaredConfig(sessions, tunnelID, credentialsFile, domain)

	yamlData, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal cloudflared config: %w", err)
	}

	// Atomic write: temp file + rename
	dir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "cloudflared-config-*.yaml")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(yamlData); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write config: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, cfgPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename config file: %w", err)
	}

	return nil
}
