package caddy

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/viper"
)

// CaddyConfig represents the full Caddy JSON configuration
type CaddyConfig struct {
	Admin CaddyAdmin `json:"admin"`
	Apps  CaddyApps  `json:"apps"`
}

// CaddyAdmin represents the admin API configuration
type CaddyAdmin struct {
	Listen string `json:"listen"`
}

// CaddyApps contains the HTTP app configuration
type CaddyApps struct {
	HTTP CaddyHTTP `json:"http"`
}

// CaddyHTTP contains the HTTP server configuration
type CaddyHTTP struct {
	Servers map[string]CaddyServer `json:"servers"`
}

// CaddyServer represents a single HTTP server
type CaddyServer struct {
	Listen []string `json:"listen"`
	Routes []Route  `json:"routes"`
}

// sanitizeDNS is the shared helper that lowercases, replaces non-alphanumeric
// characters with hyphens, collapses runs of hyphens, and trims leading/trailing
// hyphens. extraReplacements are applied before the character-level pass.
func sanitizeDNS(s string, extraReplacements ...string) string {
	normalized := strings.ToLower(s)
	for _, r := range extraReplacements {
		normalized = strings.ReplaceAll(normalized, r, "-")
	}
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")

	var result strings.Builder
	for _, r := range normalized {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteRune('-')
		}
	}

	final := strings.Trim(result.String(), "-")
	for strings.Contains(final, "--") {
		final = strings.ReplaceAll(final, "--", "-")
	}
	return final
}

// NormalizeDNSName converts a service name to be DNS-compatible
func NormalizeDNSName(serviceName string) string {
	return sanitizeDNS(serviceName)
}

// SanitizeHostname converts a session name to be hostname-compatible.
// Unlike NormalizeDNSName, it also converts slashes to hyphens (for branch names like "feature/foo").
func SanitizeHostname(sessionName string) string {
	return sanitizeDNS(sessionName, "/")
}

// BuildCaddyConfig generates the complete Caddy JSON config from session data
func BuildCaddyConfig(sessions map[string]*SessionInfo) CaddyConfig {
	adminListen := viper.GetString("caddy_admin")
	if adminListen == "" {
		adminListen = "localhost:2019"
	}

	routes := buildRoutes(sessions)

	return CaddyConfig{
		Admin: CaddyAdmin{Listen: adminListen},
		Apps: CaddyApps{
			HTTP: CaddyHTTP{
				Servers: map[string]CaddyServer{
					"devx": {
						Listen: []string{":80"},
						Routes: routes,
					},
				},
			},
		},
	}
}

// truncateRFC1035Label truncates a DNS label to at most 63 characters (RFC 1035
// limit) while preserving the service suffix and appending a 4-char hash of the
// full label to maintain uniqueness.
func truncateRFC1035Label(label, serviceSuffix string) string {
	const maxLen = 63
	// Mask CRC32 to 16 bits for a compact 4-char hex suffix. Collision
	// probability is negligible at typical session counts (~1% at ~36 sessions).
	hash := fmt.Sprintf("%04x", crc32.ChecksumIEEE([]byte(label))&0xffff)
	// suffix shape: "-<hash>-<service>"
	suffix := "-" + hash + "-" + serviceSuffix
	if len(suffix) >= maxLen {
		// Degenerate: service name itself is absurdly long; just hash-truncate.
		return strings.TrimRight(label[:maxLen-5], "-") + "-" + hash
	}
	prefixLen := maxLen - len(suffix)
	prefix := strings.TrimRight(label[:prefixLen], "-")
	return prefix + suffix
}

// BuildHostname constructs the hostname for a session/service combination.
// Returns "" if the service name normalizes to empty.
// Labels are capped at 63 characters per RFC 1035; longer labels are truncated
// with a 4-character hash appended to preserve uniqueness.
func BuildHostname(sessionName, serviceName, projectAlias string) string {
	dnsService := NormalizeDNSName(serviceName)
	if dnsService == "" {
		return ""
	}
	sanitizedSession := SanitizeHostname(sessionName)
	var label string
	if projectAlias != "" {
		sanitizedProject := NormalizeDNSName(projectAlias)
		label = fmt.Sprintf("%s-%s-%s", sanitizedProject, sanitizedSession, dnsService)
	} else {
		label = fmt.Sprintf("%s-%s", sanitizedSession, dnsService)
	}
	if len(label) > 63 {
		label = truncateRFC1035Label(label, dnsService)
	}
	return label + ".localhost"
}

// BuildRouteID constructs the route ID for a session/service combination.
// Returns "" if the service name normalizes to empty.
func BuildRouteID(sessionName, serviceName, projectAlias string) string {
	dnsService := NormalizeDNSName(serviceName)
	if dnsService == "" {
		return ""
	}
	sanitizedSession := SanitizeHostname(sessionName)
	if projectAlias != "" {
		sanitizedProject := NormalizeDNSName(projectAlias)
		return fmt.Sprintf("sess-%s-%s-%s", sanitizedProject, sanitizedSession, dnsService)
	}
	return fmt.Sprintf("sess-%s-%s", sanitizedSession, dnsService)
}

// buildRoutes generates all session routes in deterministic order
func buildRoutes(sessions map[string]*SessionInfo) []Route {
	var routes []Route

	// Sort session names for deterministic output
	sessionNames := make([]string, 0, len(sessions))
	for name := range sessions {
		sessionNames = append(sessionNames, name)
	}
	sort.Strings(sessionNames)

	for _, sessionName := range sessionNames {
		info := sessions[sessionName]

		// Sort service names for deterministic output
		serviceNames := make([]string, 0, len(info.Ports))
		for svc := range info.Ports {
			serviceNames = append(serviceNames, svc)
		}
		sort.Strings(serviceNames)

		for _, serviceName := range serviceNames {
			port := info.Ports[serviceName]
			hostname := BuildHostname(sessionName, serviceName, info.ProjectAlias)
			if hostname == "" {
				continue
			}
			routeID := BuildRouteID(sessionName, serviceName, info.ProjectAlias)

			routes = append(routes, Route{
				ID: routeID,
				Match: []RouteMatch{
					{Host: []string{hostname}},
				},
				Handle: []RouteHandler{
					{
						Handler:   "reverse_proxy",
						Upstreams: []RouteUpstream{{Dial: fmt.Sprintf("localhost:%d", port)}},
					},
				},
				Terminal: true,
			})
		}
	}

	if routes == nil {
		routes = []Route{}
	}

	return routes
}

// configPath returns the path to the generated Caddy config file
func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "devx", "caddy-config.json")
}

// SyncRoutes generates the Caddy config file and reloads Caddy.
// It writes the config even if Caddy is not running, so the next
// Caddy start picks up the correct routes.
func SyncRoutes(sessions map[string]*SessionInfo) error {
	if viper.GetBool("disable_caddy") {
		return nil
	}

	config := BuildCaddyConfig(sessions)

	cfgPath := configPath()
	if cfgPath == "" {
		return fmt.Errorf("could not determine config path")
	}

	// Marshal config
	jsonData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal Caddy config: %w", err)
	}

	// Atomic write: temp file + rename
	dir := filepath.Dir(cfgPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	tmpFile, err := os.CreateTemp(dir, "caddy-config-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(jsonData); err != nil {
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

	// Try to reload Caddy
	if err := reloadCaddy(cfgPath); err != nil {
		fmt.Printf("Warning: Caddy reload failed (config saved for next start): %v\n", err)
	}

	return nil
}

// reloadCaddy runs `caddy reload` pointing at the config file.
func reloadCaddy(cfgPath string) error {
	cmd := exec.Command("caddy", "reload", "--config", cfgPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(output))
	}
	return nil
}
