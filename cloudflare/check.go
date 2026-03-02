package cloudflare

import (
	"fmt"
	"net"
	"os"
	"os/exec"

	"github.com/jfox85/devx/caddy"
	"gopkg.in/yaml.v3"
)

// TunnelCheckResult holds the result of a cloudflare tunnel health check
type TunnelCheckResult struct {
	BinaryInstalled   bool
	TunnelExists      bool
	TunnelExistsError string
	ConfigExists      bool
	ConfigValid       bool
	ConfigError       string
	IngressMismatch   bool
	MissingRules      []string
	DNSValid          bool
	DNSError          string
}

// CheckTunnel performs a comprehensive health check of the cloudflare tunnel setup.
func CheckTunnel(sessions map[string]*caddy.SessionInfo, tunnelID, domain, cfgPath string) TunnelCheckResult {
	result := TunnelCheckResult{}
	cfgPath = expandPath(cfgPath)

	// Check binary
	_, err := exec.LookPath("cloudflared")
	result.BinaryInstalled = err == nil

	// Check if tunnel is registered in Cloudflare's account
	if result.BinaryInstalled && tunnelID != "" {
		cmd := exec.Command("cloudflared", "tunnel", "info", tunnelID)
		if err := cmd.Run(); err != nil {
			result.TunnelExists = false
			result.TunnelExistsError = fmt.Sprintf("cloudflared tunnel info failed: %v", err)
		} else {
			result.TunnelExists = true
		}
	}

	// Check config file
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		result.ConfigExists = false
		return result
	}
	result.ConfigExists = true

	var existing CloudflaredConfig
	if err := yaml.Unmarshal(data, &existing); err != nil {
		result.ConfigValid = false
		result.ConfigError = err.Error()
		return result
	}
	result.ConfigValid = true

	// Check ingress rules match current sessions
	expected := buildCloudflaredConfig(sessions, tunnelID, "", domain)
	expectedHosts := make(map[string]bool)
	for _, rule := range expected.Ingress {
		if rule.Hostname != "" {
			expectedHosts[rule.Hostname] = false
		}
	}
	for _, rule := range existing.Ingress {
		if rule.Hostname != "" {
			expectedHosts[rule.Hostname] = true
		}
	}
	for host, found := range expectedHosts {
		if !found {
			result.IngressMismatch = true
			result.MissingRules = append(result.MissingRules, host)
		}
	}

	// Check DNS (wildcard lookup)
	if domain != "" {
		testHost := "devx-check." + domain
		addrs, err := net.LookupHost(testHost)
		if err != nil || len(addrs) == 0 {
			result.DNSValid = false
			result.DNSError = fmt.Sprintf("DNS lookup for %s failed: %v", testHost, err)
		} else {
			result.DNSValid = true
		}
	}

	return result
}
