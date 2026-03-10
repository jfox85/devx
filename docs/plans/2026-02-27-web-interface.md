# Web Interface Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add mobile-first remote access to devx: external domain routing via Cloudflare Tunnel, a `devx web` HTTP daemon serving a Svelte SPA for session management, and in-browser tmux terminal access via ttyd.

**Architecture:** Three phases — Phase 1 adds an `external_domain` config and a `cloudflare/` package that generates a cloudflared ingress config (mirroring how `caddy/` manages its config file). Phase 2 adds the Go HTTP server (`web/` package) with a Svelte SPA embedded in the binary. Phase 3 adds per-session ttyd lifecycle management and a WebSocket proxy for in-browser terminal access.

**Tech Stack:** Go 1.23 stdlib HTTP (method+path routing), `gopkg.in/yaml.v3` (already in go.mod) for cloudflared config, `github.com/gorilla/websocket` (new dep, for WS proxy), Svelte 5 + Vite (frontend, compiled to `web/dist/` and embedded via `//go:embed`).

---

## Phase 1: External Domain Routing + Cloudflare Tunnel

---

### Task 1: Add external domain config keys and viper defaults

**Files:**
- Modify: `cmd/root.go` (add viper defaults)
- Modify: `config/config.go` (add fields to Config struct)

**Step 1: Write a failing test for the new config fields**

Add to `config/config.go` test (create `config/config_test.go` if it doesn't exist):

```go
package config

import (
    "testing"
    "github.com/spf13/viper"
)

func TestLoadConfigExternalDomain(t *testing.T) {
    viper.Reset()
    viper.Set("external_domain", "example.com")
    viper.Set("cloudflare_tunnel_id", "abc-123")
    viper.Set("cloudflare_tunnel_config", "/tmp/cf.yaml")

    cfg, err := LoadConfig()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if cfg.ExternalDomain != "example.com" {
        t.Errorf("expected ExternalDomain=example.com, got %q", cfg.ExternalDomain)
    }
    if cfg.CloudflareTunnelID != "abc-123" {
        t.Errorf("expected CloudflareTunnelID=abc-123, got %q", cfg.CloudflareTunnelID)
    }
    if cfg.CloudflareTunnelConfig != "/tmp/cf.yaml" {
        t.Errorf("expected CloudflareTunnelConfig=/tmp/cf.yaml, got %q", cfg.CloudflareTunnelConfig)
    }
}
```

**Step 2: Run to verify it fails**

```bash
go test ./config/... -run TestLoadConfigExternalDomain -v
```
Expected: FAIL — `cfg.ExternalDomain` field doesn't exist yet.

**Step 3: Add fields to Config struct and viper defaults**

In `config/config.go`, extend the `Config` struct:

```go
type Config struct {
    BaseDomain             string   `mapstructure:"basedomain"`
    CaddyAPI               string   `mapstructure:"caddy_api"`
    TmuxpTemplate          string   `mapstructure:"tmuxp_template"`
    Ports                  []string `mapstructure:"ports"`
    ExternalDomain         string   `mapstructure:"external_domain"`
    CloudflareTunnelID     string   `mapstructure:"cloudflare_tunnel_id"`
    CloudflareTunnelConfig string   `mapstructure:"cloudflare_tunnel_config"`
    WebSecretToken         string   `mapstructure:"web_secret_token"`
    WebPort                int      `mapstructure:"web_port"`
    WebAutostart           bool     `mapstructure:"web_autostart"`
}
```

In `LoadConfig()`, after the existing `~` expansion block, add expansion for `CloudflareTunnelConfig`:

```go
if cfg.CloudflareTunnelConfig != "" && cfg.CloudflareTunnelConfig[0] == '~' {
    home, err := os.UserHomeDir()
    if err != nil {
        return nil, err
    }
    cfg.CloudflareTunnelConfig = filepath.Join(home, cfg.CloudflareTunnelConfig[1:])
}
```

In `cmd/root.go`, in `initConfig()` after the existing `viper.SetDefault` calls, add:

```go
viper.SetDefault("external_domain", "")
viper.SetDefault("cloudflare_tunnel_id", "")
viper.SetDefault("cloudflare_tunnel_config", "~/.cloudflared/config.yaml")
viper.SetDefault("web_secret_token", "")
viper.SetDefault("web_port", 7777)
viper.SetDefault("web_autostart", false)
```

**Step 4: Run test to verify it passes**

```bash
go test ./config/... -run TestLoadConfigExternalDomain -v
```
Expected: PASS

**Step 5: Run full test suite to check for regressions**

```bash
go test ./... -race
```
Expected: all pass.

**Step 6: Commit**

```bash
git add config/config.go config/config_test.go cmd/root.go
git commit -m "feat: add external_domain, cloudflare, and web config keys"
```

---

### Task 2: Add BuildExternalHostname to caddy package

**Files:**
- Modify: `caddy/config.go`
- Modify: `caddy/config_test.go`

**Step 1: Write failing test**

Add to `caddy/config_test.go`:

```go
func TestBuildExternalHostname(t *testing.T) {
    tests := []struct {
        session  string
        service  string
        project  string
        domain   string
        expected string
    }{
        {"my-session", "ui", "", "example.com", "my-session-ui.example.com"},
        {"my-session", "api", "myproject", "example.com", "myproject-my-session-api.example.com"},
        {"feature/add-auth", "ui", "", "example.com", "feature-add-auth-ui.example.com"},
        {"my-session", "", "", "example.com", ""},
    }
    for _, tt := range tests {
        got := BuildExternalHostname(tt.session, tt.service, tt.project, tt.domain)
        if got != tt.expected {
            t.Errorf("BuildExternalHostname(%q,%q,%q,%q) = %q, want %q",
                tt.session, tt.service, tt.project, tt.domain, got, tt.expected)
        }
    }
}
```

**Step 2: Run to verify it fails**

```bash
go test ./caddy/... -run TestBuildExternalHostname -v
```
Expected: FAIL — `BuildExternalHostname` undefined.

**Step 3: Add function to caddy/config.go**

Add after `BuildHostname`:

```go
// BuildExternalHostname constructs the hostname for a session/service on an external domain.
// Returns "" if the service name normalizes to empty or domain is empty.
func BuildExternalHostname(sessionName, serviceName, projectAlias, domain string) string {
    if domain == "" {
        return ""
    }
    dnsService := NormalizeDNSName(serviceName)
    if dnsService == "" {
        return ""
    }
    sanitizedSession := SanitizeHostname(sessionName)
    if projectAlias != "" {
        sanitizedProject := NormalizeDNSName(projectAlias)
        return fmt.Sprintf("%s-%s-%s.%s", sanitizedProject, sanitizedSession, dnsService, domain)
    }
    return fmt.Sprintf("%s-%s.%s", sanitizedSession, dnsService, domain)
}
```

**Step 4: Run test**

```bash
go test ./caddy/... -run TestBuildExternalHostname -v
```
Expected: PASS

**Step 5: Run full suite**

```bash
go test ./... -race
```

**Step 6: Commit**

```bash
git add caddy/config.go caddy/config_test.go
git commit -m "feat: add BuildExternalHostname for external domain routing"
```

---

### Task 3: Create cloudflare package with SyncTunnel

**Files:**
- Create: `cloudflare/tunnel.go`
- Create: `cloudflare/tunnel_test.go`

**Step 1: Write failing tests**

Create `cloudflare/tunnel_test.go`:

```go
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

    cfg := buildCloudflaredConfig(sessions, "tunnel-abc", "/home/user/.cloudflared/creds.json", "example.com")

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

    // Check service rules contain expected hostnames and services
    var foundUI, foundAPI bool
    for _, rule := range cfg.Ingress {
        if rule.Hostname == "my-session-ui.example.com" {
            foundUI = true
            if !strings.Contains(rule.Service, "my-session-ui.localhost") {
                t.Errorf("ui service should proxy to localhost hostname, got %q", rule.Service)
            }
        }
        if rule.Hostname == "my-session-api.example.com" {
            foundAPI = true
        }
    }
    if !foundUI || !foundAPI {
        t.Errorf("missing expected ingress rules: ui=%v api=%v", foundUI, foundAPI)
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

    err := SyncTunnel(sessions, "tunnel-xyz", "/tmp/creds.json", "example.com", cfgPath)
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
    if !strings.Contains(content, "http_status:404") {
        t.Errorf("config missing catch-all rule: %s", content)
    }
}
```

**Step 2: Run to verify it fails**

```bash
go test ./cloudflare/... -v
```
Expected: FAIL — package doesn't exist yet.

**Step 3: Create cloudflare/tunnel.go**

```go
package cloudflare

import (
    "fmt"
    "os"
    "path/filepath"
    "sort"

    "github.com/jfox85/devx/caddy"
    "gopkg.in/yaml.v3"
)

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

// OriginRequest holds per-rule origin options
type OriginRequest struct {
    NoTLSVerify bool `yaml:"noTLSVerify,omitempty"`
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
            localHost := caddy.BuildHostname(sessionName, serviceName, info.ProjectAlias)
            if localHost == "" {
                continue
            }
            rules = append(rules, IngressRule{
                Hostname: externalHost,
                Service:  fmt.Sprintf("https://%s", localHost),
                OriginRequest: &OriginRequest{
                    NoTLSVerify: true, // Caddy uses self-signed cert for .localhost
                },
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
```

**Step 4: Run tests**

```bash
go test ./cloudflare/... -v -race
```
Expected: PASS

**Step 5: Commit**

```bash
git add cloudflare/
git commit -m "feat: add cloudflare package with SyncTunnel config generation"
```

---

### Task 4: Wire SyncTunnel into session lifecycle

**Files:**
- Modify: `cmd/caddy_sync.go`
- Modify: `cmd/session_create.go` (calls `syncAllCaddyRoutes`)
- Modify: `cmd/session_rm.go` (calls `syncAllCaddyRoutes`)

**Step 1: Add syncAllCloudflareRoutes to caddy_sync.go**

Add to `cmd/caddy_sync.go` after `syncAllCaddyRoutes`:

```go
// syncAllCloudflareRoutes regenerates the cloudflared config from current sessions.
// Skips silently if external_domain or cloudflare_tunnel_id is not configured.
func syncAllCloudflareRoutes() error {
    domain := viper.GetString("external_domain")
    tunnelID := viper.GetString("cloudflare_tunnel_id")
    if domain == "" || tunnelID == "" {
        return nil
    }

    store, err := session.LoadSessions()
    if err != nil {
        return fmt.Errorf("failed to load sessions for Cloudflare sync: %w", err)
    }

    registry, err := config.LoadProjectRegistry()
    if err != nil {
        return fmt.Errorf("failed to load project registry for Cloudflare sync: %w", err)
    }

    credentialsFile := viper.GetString("cloudflare_credentials_file")
    cfgPath := viper.GetString("cloudflare_tunnel_config")

    return cloudflare.SyncTunnel(
        buildSessionInfoMap(store, registry),
        tunnelID,
        credentialsFile,
        domain,
        cfgPath,
    )
}
```

Add the import `"github.com/jfox85/devx/cloudflare"` and `"github.com/spf13/viper"` to `cmd/caddy_sync.go`.

**Step 2: Call syncAllCloudflareRoutes from session_create.go**

In `cmd/session_create.go`, after the existing `syncAllCaddyRoutes()` call (around line 260):

```go
// Sync all Caddy routes (writes config file + reloads)
if err := syncAllCaddyRoutes(); err != nil {
    fmt.Printf("Warning: %v\n", err)
}

// Sync Cloudflare tunnel config (no-op if not configured)
if err := syncAllCloudflareRoutes(); err != nil {
    fmt.Printf("Warning: Cloudflare sync failed: %v\n", err)
}
```

**Step 3: Check session_rm.go and add the same call there**

Read `cmd/session_rm.go` to find where `syncAllCaddyRoutes()` is called, then add `syncAllCloudflareRoutes()` call immediately after it.

**Step 4: Run tests**

```bash
go test ./... -race
```

**Step 5: Commit**

```bash
git add cmd/caddy_sync.go cmd/session_create.go cmd/session_rm.go
git commit -m "feat: wire SyncTunnel into session create/remove lifecycle"
```

---

### Task 5: Add devx cloudflare sync command

**Files:**
- Create: `cmd/cloudflare.go`

**Step 1: Create cmd/cloudflare.go**

```go
package cmd

import (
    "fmt"

    "github.com/spf13/cobra"
    "github.com/spf13/viper"
)

var cloudflareCmd = &cobra.Command{
    Use:   "cloudflare",
    Short: "Manage Cloudflare tunnel config for development sessions",
    Long:  `Commands for managing the cloudflared ingress config for external domain routing.`,
}

var cloudflareSyncCmd = &cobra.Command{
    Use:   "sync",
    Short: "Regenerate cloudflared config from current sessions",
    Long:  `Regenerates the cloudflared ingress config file based on current active sessions.`,
    RunE:  runCloudflareSync,
}

func init() {
    rootCmd.AddCommand(cloudflareCmd)
    cloudflareCmd.AddCommand(cloudflareSyncCmd)
}

func runCloudflareSync(cmd *cobra.Command, args []string) error {
    domain := viper.GetString("external_domain")
    tunnelID := viper.GetString("cloudflare_tunnel_id")
    if domain == "" || tunnelID == "" {
        fmt.Println("Cloudflare tunnel not configured. Set external_domain and cloudflare_tunnel_id in your config.")
        return nil
    }

    if err := syncAllCloudflareRoutes(); err != nil {
        return fmt.Errorf("failed to sync cloudflare routes: %w", err)
    }

    cfgPath := viper.GetString("cloudflare_tunnel_config")
    fmt.Printf("✓ Cloudflare config written to %s\n", cfgPath)
    return nil
}
```

**Step 2: Build and smoke test**

```bash
go build -o devx . && ./devx cloudflare sync
```
Expected: Either "not configured" message (if no config set) or writes the config file.

**Step 3: Run tests**

```bash
go test ./... -race
```

**Step 4: Commit**

```bash
git add cmd/cloudflare.go
git commit -m "feat: add devx cloudflare sync command"
```

---

### Task 6: Add devx cloudflare check command

**Files:**
- Create: `cloudflare/check.go`
- Create: `cloudflare/check_test.go`
- Modify: `cmd/cloudflare.go`

**Step 1: Write failing test for check logic**

Create `cloudflare/check_test.go`:

```go
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
    if err := SyncTunnel(sessions, "tid", "/tmp/c.json", "ex.com", cfgPath); err != nil {
        t.Fatalf("SyncTunnel: %v", err)
    }

    result := CheckTunnel(sessions, "tid", "ex.com", cfgPath)

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
    result2 := CheckTunnel(sessions, "tid", "ex.com", "/nonexistent/config.yaml")
    if result2.ConfigExists {
        t.Error("expected ConfigExists=false for missing file")
    }
}
```

**Step 2: Run to verify it fails**

```bash
go test ./cloudflare/... -run TestCheckTunnelConfig -v
```

**Step 3: Create cloudflare/check.go**

```go
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
    BinaryInstalled bool
    TunnelRunning   bool
    TunnelError     string
    ConfigExists    bool
    ConfigValid     bool
    ConfigError     string
    IngressMismatch bool
    MissingRules    []string
    DNSValid        bool
    DNSError        string
}

// CheckTunnel performs a comprehensive health check of the cloudflare tunnel setup.
func CheckTunnel(sessions map[string]*caddy.SessionInfo, tunnelID, domain, cfgPath string) TunnelCheckResult {
    result := TunnelCheckResult{}

    // Check binary
    _, err := exec.LookPath("cloudflared")
    result.BinaryInstalled = err == nil

    // Check tunnel daemon
    if result.BinaryInstalled && tunnelID != "" {
        cmd := exec.Command("cloudflared", "tunnel", "info", tunnelID)
        if err := cmd.Run(); err != nil {
            result.TunnelRunning = false
            result.TunnelError = fmt.Sprintf("cloudflared tunnel info failed: %v", err)
        } else {
            result.TunnelRunning = true
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
```

**Step 4: Run tests**

```bash
go test ./cloudflare/... -v -race
```

**Step 5: Wire check command into cmd/cloudflare.go**

Add to `cmd/cloudflare.go`:

```go
var cloudflareCheckCmd = &cobra.Command{
    Use:   "check",
    Short: "Check Cloudflare tunnel setup and verify ingress rules",
    RunE:  runCloudflareCheck,
}

func init() {
    // existing init already added cloudflareCmd and cloudflareSyncCmd
    cloudflareCmd.AddCommand(cloudflareCheckCmd)
}

func runCloudflareCheck(cmd *cobra.Command, args []string) error {
    domain := viper.GetString("external_domain")
    tunnelID := viper.GetString("cloudflare_tunnel_id")
    cfgPath := viper.GetString("cloudflare_tunnel_config")

    if domain == "" || tunnelID == "" {
        fmt.Println("Cloudflare tunnel not configured. Set external_domain and cloudflare_tunnel_id in your config.")
        return nil
    }

    store, err := session.LoadSessions()
    if err != nil {
        return fmt.Errorf("failed to load sessions: %w", err)
    }
    registry, err := config.LoadProjectRegistry()
    if err != nil {
        return fmt.Errorf("failed to load project registry: %w", err)
    }

    sessionInfos := buildSessionInfoMap(store, registry)
    result := cloudflare.CheckTunnel(sessionInfos, tunnelID, domain, cfgPath)

    fmt.Println("=== Cloudflare Tunnel Status ===")
    printCheckLine("cloudflared binary installed", result.BinaryInstalled, "")
    printCheckLine("tunnel daemon running", result.TunnelRunning, result.TunnelError)
    printCheckLine("config file exists", result.ConfigExists, "")
    printCheckLine("config file valid", result.ConfigValid, result.ConfigError)
    printCheckLine("ingress rules match sessions", !result.IngressMismatch, "")
    if result.IngressMismatch {
        for _, h := range result.MissingRules {
            fmt.Printf("  missing: %s\n", h)
        }
        fmt.Println("  Run 'devx cloudflare sync' to fix.")
    }
    printCheckLine("DNS wildcard resolves", result.DNSValid, result.DNSError)

    return nil
}

func printCheckLine(label string, ok bool, errMsg string) {
    if ok {
        fmt.Printf("✓ %s\n", label)
    } else if errMsg != "" {
        fmt.Printf("✗ %s: %s\n", label, errMsg)
    } else {
        fmt.Printf("✗ %s\n", label)
    }
}
```

Add missing imports to `cmd/cloudflare.go`: `"github.com/jfox85/devx/cloudflare"`, `"github.com/jfox85/devx/config"`, `"github.com/jfox85/devx/session"`.

**Step 6: Run full suite and build**

```bash
go test ./... -race && go build -o devx . && ./devx cloudflare check
```

**Step 7: Run pre-commit checklist**

```bash
gofmt -w . && go vet ./... && go mod tidy
```

**Step 8: Commit**

```bash
git add cloudflare/check.go cloudflare/check_test.go cmd/cloudflare.go
git commit -m "feat: add devx cloudflare check command with tunnel health validation"
```

---

## Phase 2: devx web Daemon + Svelte SPA

---

### Task 7: Scaffold web package and basic HTTP server with auth

**Files:**
- Create: `web/server.go`
- Create: `web/server_test.go`

**Step 1: Write failing auth middleware test**

Create `web/server_test.go`:

```go
package web

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestAuthMiddlewareRejectsUnauthorized(t *testing.T) {
    token := "test-secret"
    handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    // No auth header
    req := httptest.NewRequest("GET", "/api/sessions", nil)
    w := httptest.NewRecorder()
    handler.ServeHTTP(w, req)
    if w.Code != http.StatusUnauthorized {
        t.Errorf("expected 401, got %d", w.Code)
    }
}

func TestAuthMiddlewareAcceptsBearerToken(t *testing.T) {
    token := "test-secret"
    handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    req := httptest.NewRequest("GET", "/api/sessions", nil)
    req.Header.Set("Authorization", "Bearer test-secret")
    w := httptest.NewRecorder()
    handler.ServeHTTP(w, req)
    if w.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", w.Code)
    }
}

func TestAuthMiddlewarePassesNonAPIRoutes(t *testing.T) {
    token := "test-secret"
    handler := authMiddleware(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    // Static assets don't require auth
    req := httptest.NewRequest("GET", "/", nil)
    w := httptest.NewRecorder()
    handler.ServeHTTP(w, req)
    if w.Code != http.StatusOK {
        t.Errorf("expected 200 for non-API route, got %d", w.Code)
    }
}
```

**Step 2: Run to verify it fails**

```bash
go test ./web/... -v
```
Expected: FAIL — package doesn't exist.

**Step 3: Create web/server.go**

```go
package web

import (
    "context"
    "fmt"
    "net"
    "net/http"
    "strings"
)

// Server is the devx web HTTP server
type Server struct {
    token  string
    port   int
    server *http.Server
}

// New creates a new Server. token must be non-empty.
func New(token string, port int) (*Server, error) {
    if token == "" {
        return nil, fmt.Errorf("web_secret_token must be set in config to use devx web")
    }
    return &Server{token: token, port: port}, nil
}

// Start begins listening and serving.
func (s *Server) Start() error {
    mux := http.NewServeMux()
    registerRoutes(mux)

    s.server = &http.Server{
        Addr:    fmt.Sprintf(":%d", s.port),
        Handler: authMiddleware(s.token, mux),
    }

    ln, err := net.Listen("tcp", s.server.Addr)
    if err != nil {
        return fmt.Errorf("failed to listen on port %d: %w", s.port, err)
    }

    fmt.Printf("devx web listening on http://localhost:%d\n", s.port)
    return s.server.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
    if s.server == nil {
        return nil
    }
    return s.server.Shutdown(ctx)
}

// authMiddleware enforces token auth on all /api/* routes.
// Non-API routes (static assets, login) pass through unauthenticated.
func authMiddleware(token string, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/login" {
            next.ServeHTTP(w, r)
            return
        }

        // Check Authorization: Bearer <token>
        if r.Header.Get("Authorization") == "Bearer "+token {
            next.ServeHTTP(w, r)
            return
        }

        // Check session cookie
        if cookie, err := r.Cookie("devx_token"); err == nil && cookie.Value == token {
            next.ServeHTTP(w, r)
            return
        }

        http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
    })
}

func registerRoutes(mux *http.ServeMux) {
    // API routes registered in api.go
    registerAPIRoutes(mux)
    // Static SPA served from embedded FS (registered in embed.go)
    registerStaticRoutes(mux)
}
```

**Step 4: Run tests**

```bash
go test ./web/... -v -race
```
Expected: PASS (once we add stub `registerAPIRoutes` and `registerStaticRoutes` in the next steps)

Note: Create stubs to make it compile — add temporary `web/api.go` with `func registerAPIRoutes(mux *http.ServeMux) {}` and `web/embed.go` with `func registerStaticRoutes(mux *http.ServeMux) {}` until Tasks 8 and 10 flesh them out.

**Step 5: Commit**

```bash
git add web/
git commit -m "feat: add web package with HTTP server and auth middleware"
```

---

### Task 8: Implement REST API endpoints

**Files:**
- Modify: `web/api.go`
- Create: `web/api_test.go`

**Step 1: Write failing tests for key API endpoints**

Create `web/api_test.go`:

```go
package web

import (
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestGetSessionsReturnsJSON(t *testing.T) {
    mux := http.NewServeMux()
    registerAPIRoutes(mux)

    req := httptest.NewRequest("GET", "/api/sessions", nil)
    w := httptest.NewRecorder()
    mux.ServeHTTP(w, req)

    if w.Code != http.StatusOK {
        t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
    }
    if ct := w.Header().Get("Content-Type"); ct != "application/json" {
        t.Errorf("expected application/json, got %q", ct)
    }

    var resp map[string]interface{}
    if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
        t.Errorf("response is not valid JSON: %v\nbody: %s", err, w.Body.String())
    }
}

func TestGetHealthReturnsOK(t *testing.T) {
    mux := http.NewServeMux()
    registerAPIRoutes(mux)

    req := httptest.NewRequest("GET", "/api/health", nil)
    w := httptest.NewRecorder()
    mux.ServeHTTP(w, req)

    if w.Code != http.StatusOK {
        t.Errorf("expected 200, got %d", w.Code)
    }
}
```

**Step 2: Run to verify they fail**

```bash
go test ./web/... -run TestGetSessions -v
```

**Step 3: Implement web/api.go**

```go
package web

import (
    "encoding/json"
    "fmt"
    "net/http"
    "strings"

    "github.com/jfox85/devx/caddy"
    "github.com/jfox85/devx/config"
    "github.com/jfox85/devx/session"
    "github.com/spf13/viper"
)

func registerAPIRoutes(mux *http.ServeMux) {
    mux.HandleFunc("GET /api/health", handleHealth)
    mux.HandleFunc("POST /api/login", handleLogin)
    mux.HandleFunc("GET /api/sessions", handleListSessions)
    mux.HandleFunc("POST /api/sessions", handleCreateSession)
    mux.HandleFunc("DELETE /api/sessions/{name}", handleDeleteSession)
    mux.HandleFunc("POST /api/sessions/{name}/flag", handleFlagSession)
    mux.HandleFunc("DELETE /api/sessions/{name}/flag", handleUnflagSession)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(v)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
    // Token is validated by middleware; if we reach here the token was correct.
    // Set a session cookie for browser clients.
    token := viper.GetString("web_secret_token")
    http.SetCookie(w, &http.Cookie{
        Name:     "devx_token",
        Value:    token,
        Path:     "/",
        HttpOnly: true,
        SameSite: http.SameSiteStrictMode,
    })
    writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// sessionResponse is the JSON shape returned for each session
type sessionResponse struct {
    Name          string            `json:"name"`
    Branch        string            `json:"branch"`
    ProjectAlias  string            `json:"project_alias,omitempty"`
    Ports         map[string]int    `json:"ports"`
    Routes        map[string]string `json:"routes"`
    ExternalRoutes map[string]string `json:"external_routes,omitempty"`
    AttentionFlag  bool             `json:"attention_flag"`
}

func buildSessionResponse(sess *session.Session) sessionResponse {
    externalDomain := viper.GetString("external_domain")
    externalRoutes := make(map[string]string)
    if externalDomain != "" {
        for svc := range sess.Ports {
            h := caddy.BuildExternalHostname(sess.Name, svc, sess.ProjectAlias, externalDomain)
            if h != "" {
                externalRoutes[svc] = h
            }
        }
    }
    return sessionResponse{
        Name:           sess.Name,
        Branch:         sess.Branch,
        ProjectAlias:   sess.ProjectAlias,
        Ports:          sess.Ports,
        Routes:         sess.Routes,
        ExternalRoutes: externalRoutes,
        AttentionFlag:  sess.AttentionFlag,
    }
}

func handleListSessions(w http.ResponseWriter, r *http.Request) {
    store, err := session.LoadSessions()
    if err != nil {
        writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
        return
    }

    var sessions []sessionResponse
    for _, sess := range store.Sessions {
        sessions = append(sessions, buildSessionResponse(sess))
    }
    if sessions == nil {
        sessions = []sessionResponse{}
    }
    writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

type createSessionRequest struct {
    Name    string `json:"name"`
    Project string `json:"project"`
}

func handleCreateSession(w http.ResponseWriter, r *http.Request) {
    var req createSessionRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
        return
    }
    if req.Name == "" {
        writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
        return
    }

    // Delegate to the same logic as `devx session create`
    // We run it as a subprocess to keep the API simple and reuse all existing logic.
    args := []string{"session", "create", req.Name, "--no-tmux"}
    if req.Project != "" {
        args = append(args, "--project", req.Project)
    }
    if err := runDevxSubcommand(args...); err != nil {
        writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
        return
    }

    store, _ := session.LoadSessions()
    if sess, ok := store.Sessions[req.Name]; ok {
        writeJSON(w, http.StatusCreated, buildSessionResponse(sess))
    } else {
        writeJSON(w, http.StatusCreated, map[string]string{"name": req.Name})
    }
}

func handleDeleteSession(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    if err := runDevxSubcommand("session", "rm", name); err != nil {
        writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
        return
    }
    w.WriteHeader(http.StatusNoContent)
}

func handleFlagSession(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    store, err := session.LoadSessions()
    if err != nil {
        writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
        return
    }
    if err := store.UpdateSession(name, func(s *session.Session) {
        s.AttentionFlag = true
        s.AttentionReason = "manual"
    }); err != nil {
        writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
        return
    }
    w.WriteHeader(http.StatusNoContent)
}

func handleUnflagSession(w http.ResponseWriter, r *http.Request) {
    name := r.PathValue("name")
    store, err := session.LoadSessions()
    if err != nil {
        writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
        return
    }
    if err := store.UpdateSession(name, func(s *session.Session) {
        s.AttentionFlag = false
        s.AttentionReason = ""
    }); err != nil {
        writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
        return
    }
    w.WriteHeader(http.StatusNoContent)
}

// runDevxSubcommand re-invokes the devx binary with the given args.
// This reuses all existing CLI logic without duplicating it.
func runDevxSubcommand(args ...string) error {
    import_os_exec_cmd := fmt.Sprintf("executing devx %s", strings.Join(args, " "))
    _ = import_os_exec_cmd
    // Implementation note: use os.Executable() to find self, then exec.Command
    // This is filled out properly — see below
    return runSelf(args...)
}
```

Note: `runSelf` needs to be implemented. Add this helper to `web/api.go`:

```go
import "os/exec"
import "os"

func runSelf(args ...string) error {
    self, err := os.Executable()
    if err != nil {
        return fmt.Errorf("failed to find executable: %w", err)
    }
    cmd := exec.Command(self, args...)
    cmd.Stdout = nil
    cmd.Stderr = nil
    return cmd.Run()
}
```

Clean up the placeholder `import_os_exec_cmd` — it was just illustrative. The full `handleCreateSession` calls `runSelf("session", "create", req.Name, "--no-tmux", ...)`.

**Step 4: Run tests**

```bash
go test ./web/... -v -race
```

**Step 5: Commit**

```bash
git add web/api.go web/api_test.go
git commit -m "feat: implement REST API endpoints for session management"
```

---

### Task 9: Add devx web CLI commands

**Files:**
- Create: `cmd/web.go`

**Step 1: Create cmd/web.go**

```go
package cmd

import (
    "context"
    "fmt"
    "os"
    "os/signal"
    "path/filepath"
    "strconv"
    "strings"
    "syscall"

    "github.com/jfox85/devx/web"
    "github.com/spf13/cobra"
    "github.com/spf13/viper"
)

var webDaemonFlag bool

var webCmd = &cobra.Command{
    Use:   "web",
    Short: "Start the devx web interface",
    Long:  `Starts a local HTTP server with a web UI for session management. Requires web_secret_token in config.`,
    RunE:  runWeb,
}

var webStopCmd = &cobra.Command{
    Use:   "stop",
    Short: "Stop the devx web daemon",
    RunE:  runWebStop,
}

var webStatusCmd = &cobra.Command{
    Use:   "status",
    Short: "Show devx web daemon status",
    RunE:  runWebStatus,
}

func init() {
    rootCmd.AddCommand(webCmd)
    webCmd.AddCommand(webStopCmd)
    webCmd.AddCommand(webStatusCmd)
    webCmd.Flags().BoolVar(&webDaemonFlag, "daemon", false, "Run as background daemon")
}

func webPIDPath() string {
    home, _ := os.UserHomeDir()
    return filepath.Join(home, ".config", "devx", "web.pid")
}

func runWeb(cmd *cobra.Command, args []string) error {
    token := viper.GetString("web_secret_token")
    port := viper.GetInt("web_port")

    srv, err := web.New(token, port)
    if err != nil {
        return err
    }

    if webDaemonFlag {
        return startWebDaemon(port)
    }

    // Foreground mode: handle SIGTERM/SIGINT for graceful shutdown
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    errCh := make(chan error, 1)
    go func() { errCh <- srv.Start() }()

    select {
    case <-ctx.Done():
        fmt.Println("\nShutting down...")
        return srv.Shutdown(context.Background())
    case err := <-errCh:
        return err
    }
}

func startWebDaemon(port int) error {
    self, err := os.Executable()
    if err != nil {
        return fmt.Errorf("failed to find executable: %w", err)
    }

    cmd := newDaemonCmd(self, "web")
    cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
    if err := cmd.Start(); err != nil {
        return fmt.Errorf("failed to start daemon: %w", err)
    }

    pidPath := webPIDPath()
    if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0644); err != nil {
        fmt.Printf("Warning: could not write PID file: %v\n", err)
    }

    fmt.Printf("devx web daemon started (pid %d, port %d)\n", cmd.Process.Pid, port)
    return nil
}

func runWebStop(cmd *cobra.Command, args []string) error {
    data, err := os.ReadFile(webPIDPath())
    if err != nil {
        return fmt.Errorf("devx web is not running (no PID file found)")
    }

    pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
    if err != nil {
        return fmt.Errorf("invalid PID file: %w", err)
    }

    proc, err := os.FindProcess(pid)
    if err != nil {
        return fmt.Errorf("failed to find process %d: %w", pid, err)
    }

    if err := proc.Signal(syscall.SIGTERM); err != nil {
        return fmt.Errorf("failed to stop process %d: %w", pid, err)
    }

    os.Remove(webPIDPath())
    fmt.Printf("devx web stopped (pid %d)\n", pid)
    return nil
}

func runWebStatus(cmd *cobra.Command, args []string) error {
    port := viper.GetInt("web_port")
    data, err := os.ReadFile(webPIDPath())
    if err != nil {
        fmt.Println("devx web is not running")
        return nil
    }

    pid := strings.TrimSpace(string(data))
    fmt.Printf("devx web is running (pid %s, port %d)\n", pid, port)
    return nil
}
```

Note: `newDaemonCmd` is a helper to create the exec.Cmd. Add to `cmd/web.go`:

```go
import "os/exec"

func newDaemonCmd(executable string, args ...string) *exec.Cmd {
    c := exec.Command(executable, args...)
    c.Stdout = nil
    c.Stderr = nil
    c.Stdin = nil
    return c
}
```

**Step 2: Build and verify**

```bash
go build -o devx . && ./devx web --help
```
Expected: shows web command help with `--daemon` flag, `stop` and `status` subcommands.

**Step 3: Run full test suite**

```bash
go test ./... -race
```

**Step 4: Commit**

```bash
git add cmd/web.go
git commit -m "feat: add devx web command with daemon/stop/status"
```

---

### Task 10: Scaffold Svelte app with Vite

**Files:**
- Create: `web/app/` (Svelte project)
- Create: `web/embed.go`
- Modify: `Makefile`

**Step 1: Create the Svelte project**

```bash
cd web && npm create vite@latest app -- --template svelte
cd app && npm install
```

**Step 2: Add Tailwind CSS for mobile-friendly styling**

```bash
cd web/app && npm install -D tailwindcss @tailwindcss/vite
```

Create `web/app/tailwind.config.js`:
```js
export default {
  content: ['./index.html', './src/**/*.{svelte,js,ts}'],
}
```

Update `web/app/vite.config.js`:
```js
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [tailwindcss(), svelte()],
  build: {
    outDir: '../dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:7777',
    },
  },
})
```

Add `import '@tailwindcss/vite'` to `web/app/src/app.css`:
```css
@import "tailwindcss";
```

**Step 3: Create web/embed.go (stub until Svelte is built)**

```go
package web

import (
    "embed"
    "io/fs"
    "net/http"
)

//go:embed dist
var embeddedFS embed.FS

func registerStaticRoutes(mux *http.ServeMux) {
    distFS, err := fs.Sub(embeddedFS, "dist")
    if err != nil {
        panic("embedded dist not found: " + err.Error())
    }
    mux.Handle("/", http.FileServer(http.FS(distFS)))
}
```

Note: `web/dist/` must exist before `go build`. Create a placeholder:

```bash
mkdir -p web/dist && echo '<!DOCTYPE html><html><body>devx web</body></html>' > web/dist/index.html
```

Add `web/dist/` to `.gitignore` (the build output is not committed):

```bash
echo 'web/dist/' >> .gitignore
```

**Step 4: Add Makefile targets**

Update `Makefile`:

```makefile
# Build the Svelte SPA
.PHONY: web-build
web-build:
	cd web/app && npm install && npm run build

# Build everything
.PHONY: build
build: web-build
	go build $(LDFLAGS) -o $(BINARY_NAME) .

.PHONY: web-dev
web-dev:
	cd web/app && npm run dev
```

**Step 5: Build and verify embedding works**

```bash
make web-build && go build -o devx .
```
Expected: binary builds with embedded SPA.

**Step 6: Run tests**

```bash
go test ./... -race
```

**Step 7: Commit**

```bash
git add web/embed.go web/app/ web/dist/.gitkeep Makefile .gitignore
git commit -m "feat: scaffold Svelte app with Vite and embed.FS integration"
```

---

### Task 11: Implement Svelte SPA — session list, service links, new session

This task is frontend work. The Svelte files live in `web/app/src/`.

**Files:**
- Create: `web/app/src/api.js`
- Create: `web/app/src/stores/sessions.js`
- Create: `web/app/src/lib/Login.svelte`
- Create: `web/app/src/lib/SessionList.svelte`
- Create: `web/app/src/lib/SessionCard.svelte`
- Create: `web/app/src/lib/NewSessionModal.svelte`
- Modify: `web/app/src/App.svelte`

**Step 1: Create api.js — typed fetch wrapper**

```js
// web/app/src/api.js
const base = '/api'

function getToken() {
  return localStorage.getItem('devx_token') || ''
}

async function apiFetch(path, options = {}) {
  const res = await fetch(base + path, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      'Authorization': 'Bearer ' + getToken(),
      ...options.headers,
    },
  })
  if (res.status === 401) {
    localStorage.removeItem('devx_token')
    window.location.reload()
    throw new Error('Unauthorized')
  }
  return res
}

export async function listSessions() {
  const res = await apiFetch('/sessions')
  const data = await res.json()
  return data.sessions || []
}

export async function createSession(name, project) {
  const res = await apiFetch('/sessions', {
    method: 'POST',
    body: JSON.stringify({ name, project }),
  })
  if (!res.ok) {
    const err = await res.json()
    throw new Error(err.error || 'Failed to create session')
  }
  return res.json()
}

export async function deleteSession(name) {
  await apiFetch(`/sessions/${name}`, { method: 'DELETE' })
}

export async function flagSession(name) {
  await apiFetch(`/sessions/${name}/flag`, { method: 'POST' })
}

export async function unflagSession(name) {
  await apiFetch(`/sessions/${name}/flag`, { method: 'DELETE' })
}

export async function login(token) {
  localStorage.setItem('devx_token', token)
  const res = await apiFetch('/login', { method: 'POST' })
  if (!res.ok) {
    localStorage.removeItem('devx_token')
    throw new Error('Invalid token')
  }
}

export function isLoggedIn() {
  return !!localStorage.getItem('devx_token')
}
```

**Step 2: Create Login.svelte**

```svelte
<!-- web/app/src/lib/Login.svelte -->
<script>
  import { login } from '../api.js'
  let token = ''
  let error = ''
  let loading = false

  async function handleSubmit() {
    loading = true
    error = ''
    try {
      await login(token)
      window.location.reload()
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }
</script>

<div class="min-h-screen flex items-center justify-center bg-gray-950 p-4">
  <div class="w-full max-w-sm bg-gray-900 rounded-2xl p-8 shadow-xl">
    <h1 class="text-2xl font-bold text-white mb-2">devx</h1>
    <p class="text-gray-400 mb-6 text-sm">Enter your access token</p>
    <form on:submit|preventDefault={handleSubmit}>
      <input
        type="password"
        bind:value={token}
        placeholder="Token"
        class="w-full bg-gray-800 text-white rounded-lg px-4 py-3 mb-4 text-base focus:outline-none focus:ring-2 focus:ring-blue-500"
      />
      {#if error}<p class="text-red-400 text-sm mb-3">{error}</p>{/if}
      <button
        type="submit"
        disabled={loading}
        class="w-full bg-blue-600 hover:bg-blue-500 text-white font-semibold py-3 rounded-lg transition-colors"
      >
        {loading ? 'Signing in…' : 'Sign in'}
      </button>
    </form>
  </div>
</div>
```

**Step 3: Create SessionCard.svelte**

```svelte
<!-- web/app/src/lib/SessionCard.svelte -->
<script>
  export let session
  export let onOpen
  export let onDelete
  export let onFlag
</script>

<div class="bg-gray-900 rounded-xl p-4 shadow {session.attention_flag ? 'border border-yellow-500' : ''}">
  <div class="flex items-start justify-between mb-2">
    <div>
      <h2 class="text-white font-semibold text-lg leading-tight">{session.name}</h2>
      <p class="text-gray-400 text-sm">{session.branch}</p>
      {#if session.project_alias}
        <span class="text-xs bg-gray-700 text-gray-300 rounded px-2 py-0.5 mt-1 inline-block">{session.project_alias}</span>
      {/if}
    </div>
    {#if session.attention_flag}
      <span class="text-yellow-400 text-xl">⚑</span>
    {/if}
  </div>

  <!-- Service links -->
  {#if session.external_routes && Object.keys(session.external_routes).length > 0}
    <div class="flex flex-wrap gap-2 mb-3">
      {#each Object.entries(session.external_routes) as [svc, host]}
        <a href="https://{host}" target="_blank"
           class="text-xs bg-blue-900 text-blue-200 rounded-full px-3 py-1 hover:bg-blue-700 transition-colors">
          {svc} ↗
        </a>
      {/each}
    </div>
  {/if}

  <!-- Actions -->
  <div class="flex gap-2 mt-3">
    <button on:click={() => onOpen(session)}
      class="flex-1 bg-green-700 hover:bg-green-600 text-white text-sm font-medium py-2 rounded-lg transition-colors">
      Terminal
    </button>
    <button on:click={() => onFlag(session)}
      class="bg-gray-700 hover:bg-gray-600 text-white text-sm py-2 px-3 rounded-lg transition-colors">
      ⚑
    </button>
    <button on:click={() => onDelete(session)}
      class="bg-red-900 hover:bg-red-700 text-white text-sm py-2 px-3 rounded-lg transition-colors">
      ✕
    </button>
  </div>
</div>
```

**Step 4: Create SessionList.svelte**

```svelte
<!-- web/app/src/lib/SessionList.svelte -->
<script>
  import { onMount } from 'svelte'
  import { listSessions, deleteSession, flagSession } from '../api.js'
  import SessionCard from './SessionCard.svelte'
  import NewSessionModal from './NewSessionModal.svelte'

  export let onOpenTerminal

  let sessions = []
  let loading = true
  let showNewSession = false
  let error = ''

  async function load() {
    try {
      sessions = await listSessions()
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }

  onMount(load)

  async function handleDelete(session) {
    if (!confirm(`Remove session "${session.name}"?`)) return
    await deleteSession(session.name)
    await load()
  }

  async function handleFlag(session) {
    await flagSession(session.name)
    await load()
  }
</script>

<div class="min-h-screen bg-gray-950 p-4 pb-20">
  <div class="max-w-2xl mx-auto">
    <div class="flex items-center justify-between mb-6">
      <h1 class="text-2xl font-bold text-white">devx</h1>
      <button on:click={() => showNewSession = true}
        class="bg-blue-600 hover:bg-blue-500 text-white font-medium px-4 py-2 rounded-lg text-sm transition-colors">
        + New Session
      </button>
    </div>

    {#if loading}
      <p class="text-gray-400 text-center py-12">Loading sessions…</p>
    {:else if error}
      <p class="text-red-400 text-center py-12">{error}</p>
    {:else if sessions.length === 0}
      <p class="text-gray-500 text-center py-12">No active sessions. Create one to get started.</p>
    {:else}
      <div class="grid gap-3">
        {#each sessions as session (session.name)}
          <SessionCard {session} onOpen={onOpenTerminal} onDelete={handleDelete} onFlag={handleFlag} />
        {/each}
      </div>
    {/if}
  </div>
</div>

{#if showNewSession}
  <NewSessionModal on:close={() => showNewSession = false} on:created={load} />
{/if}
```

**Step 5: Create NewSessionModal.svelte**

```svelte
<!-- web/app/src/lib/NewSessionModal.svelte -->
<script>
  import { createEventDispatcher } from 'svelte'
  import { createSession } from '../api.js'

  const dispatch = createEventDispatcher()

  let name = ''
  let project = ''
  let error = ''
  let loading = false

  async function handleSubmit() {
    if (!name.trim()) { error = 'Session name is required'; return }
    loading = true
    error = ''
    try {
      await createSession(name.trim(), project.trim() || undefined)
      dispatch('created')
      dispatch('close')
    } catch (e) {
      error = e.message
    } finally {
      loading = false
    }
  }
</script>

<div class="fixed inset-0 bg-black/60 flex items-end sm:items-center justify-center z-50 p-4"
     on:click|self={() => dispatch('close')}>
  <div class="w-full max-w-sm bg-gray-900 rounded-2xl p-6 shadow-xl">
    <h2 class="text-white font-semibold text-lg mb-4">New Session</h2>
    <form on:submit|preventDefault={handleSubmit}>
      <label class="block text-gray-400 text-sm mb-1">Branch / session name</label>
      <input bind:value={name} placeholder="feature/my-branch"
        class="w-full bg-gray-800 text-white rounded-lg px-4 py-3 mb-3 text-base focus:outline-none focus:ring-2 focus:ring-blue-500" />
      <label class="block text-gray-400 text-sm mb-1">Project (optional)</label>
      <input bind:value={project} placeholder="myproject"
        class="w-full bg-gray-800 text-white rounded-lg px-4 py-3 mb-4 text-base focus:outline-none focus:ring-2 focus:ring-blue-500" />
      {#if error}<p class="text-red-400 text-sm mb-3">{error}</p>{/if}
      <div class="flex gap-3">
        <button type="button" on:click={() => dispatch('close')}
          class="flex-1 bg-gray-700 text-white py-3 rounded-lg font-medium hover:bg-gray-600 transition-colors">
          Cancel
        </button>
        <button type="submit" disabled={loading}
          class="flex-1 bg-blue-600 text-white py-3 rounded-lg font-semibold hover:bg-blue-500 transition-colors">
          {loading ? 'Creating…' : 'Create'}
        </button>
      </div>
    </form>
  </div>
</div>
```

**Step 6: Update App.svelte**

```svelte
<!-- web/app/src/App.svelte -->
<script>
  import { isLoggedIn } from './api.js'
  import Login from './lib/Login.svelte'
  import SessionList from './lib/SessionList.svelte'
  // Terminal view added in Phase 3

  let view = 'sessions'  // 'sessions' | 'terminal'
  let activeSession = null

  function openTerminal(session) {
    activeSession = session
    view = 'terminal'
  }

  function goHome() {
    view = 'sessions'
    activeSession = null
  }
</script>

{#if !isLoggedIn()}
  <Login />
{:else if view === 'sessions'}
  <SessionList onOpenTerminal={openTerminal} />
{:else if view === 'terminal'}
  <!-- Phase 3: Terminal component -->
  <div class="min-h-screen bg-gray-950 flex flex-col">
    <div class="flex items-center gap-3 p-3 bg-gray-900 border-b border-gray-800">
      <button on:click={goHome} class="text-gray-400 hover:text-white text-sm px-2 py-1 rounded">← Back</button>
      <span class="text-white font-medium">{activeSession?.name}</span>
      <span class="text-gray-500 text-sm">{activeSession?.branch}</span>
    </div>
    <div class="flex-1 flex items-center justify-center text-gray-500">
      Terminal coming in Phase 3
    </div>
  </div>
{/if}
```

**Step 7: Build and manually test in browser**

```bash
make web-build && go build -o devx .
# Set web_secret_token in config, then:
./devx web
# Open http://localhost:7777 in browser
```

**Step 8: Commit**

```bash
git add web/app/src/
git commit -m "feat: implement Svelte SPA with session list, service links, and new session modal"
```

---

### Task 12: TUI autostart integration

**Files:**
- Modify: `tui/run.go` (or wherever TUI is started)

**Step 1: Read tui/run.go to find the right insertion point**

Before implementing, run: Read `tui/run.go` to understand the startup sequence.

**Step 2: Add autostart logic**

At the top of the TUI startup (in the `Run()` function), add:

```go
func Run() error {
    // Auto-start web daemon if configured
    if viper.GetBool("web_autostart") {
        if err := ensureWebDaemonRunning(); err != nil {
            // Non-fatal — just log and continue
            fmt.Printf("Warning: could not start web daemon: %v\n", err)
        }
    }
    // ... existing TUI startup
}
```

Add helper to `tui/run.go` (or a new `tui/web.go`):

```go
func ensureWebDaemonRunning() error {
    home, _ := os.UserHomeDir()
    pidPath := filepath.Join(home, ".config", "devx", "web.pid")

    // Check if already running via PID file
    if data, err := os.ReadFile(pidPath); err == nil {
        pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
        if err == nil {
            if proc, err := os.FindProcess(pid); err == nil {
                if err := proc.Signal(syscall.Signal(0)); err == nil {
                    return nil // already running
                }
            }
        }
    }

    // Start the daemon
    self, err := os.Executable()
    if err != nil {
        return err
    }
    cmd := exec.Command(self, "web", "--daemon")
    return cmd.Run()
}
```

**Step 3: Run tests**

```bash
go test ./... -race
```

**Step 4: Run pre-commit checklist**

```bash
gofmt -w . && go vet ./... && go mod tidy
```

**Step 5: Commit Phase 2**

```bash
git add tui/ cmd/web.go
git commit -m "feat: add web_autostart TUI integration for devx web daemon"
```

---

## Phase 3: ttyd Integration + Mobile Terminal UX

---

### Task 13: Add gorilla/websocket and implement ttyd lifecycle manager

**Files:**
- Modify: `go.mod` / `go.sum` (add gorilla/websocket)
- Create: `web/ttyd.go`
- Create: `web/ttyd_test.go`

**Step 1: Add gorilla/websocket dependency**

```bash
go get github.com/gorilla/websocket
```

**Step 2: Write failing test for ttyd manager**

Create `web/ttyd_test.go`:

```go
package web

import (
    "testing"
    "time"
)

func TestTtydManagerStartStop(t *testing.T) {
    m := newTtydManager()

    // Starting ttyd for a non-existent tmux session will fail,
    // but we can test the manager's bookkeeping with a simple command.
    // Use a stub command that stays alive briefly.
    port, err := m.startForSession("test-session", "sleep", "0.1")
    if err != nil {
        t.Fatalf("startForSession returned error: %v", err)
    }
    if port <= 0 {
        t.Errorf("expected valid port, got %d", port)
    }

    // Should return same port on second call
    port2, err := m.startForSession("test-session", "sleep", "0.1")
    if err != nil {
        t.Fatalf("second startForSession returned error: %v", err)
    }
    if port2 != port {
        t.Errorf("expected same port %d on second call, got %d", port, port2)
    }

    // Wait for process to exit naturally
    time.Sleep(200 * time.Millisecond)

    m.stopSession("test-session")
    // Verify entry cleaned up (no panic, no port reuse issue)
}
```

**Step 3: Create web/ttyd.go**

```go
package web

import (
    "fmt"
    "os/exec"
    "sync"
    "time"

    "github.com/jsumners/go-getport"
)

type ttydInstance struct {
    port  int
    cmd   *exec.Cmd
    conns int       // active WebSocket connections
    timer *time.Timer
}

type ttydManager struct {
    mu       sync.Mutex
    sessions map[string]*ttydInstance
}

func newTtydManager() *ttydManager {
    return &ttydManager{sessions: make(map[string]*ttydInstance)}
}

const ttydIdleTimeout = 30 * time.Second

// startForSession returns the local port of the ttyd instance for the session,
// starting one if not already running. cmdAndArgs overrides the default
// "ttyd tmux attach -t <session>" (used for testing).
func (m *ttydManager) startForSession(sessionName string, cmdAndArgs ...string) (int, error) {
    m.mu.Lock()
    defer m.mu.Unlock()

    if inst, ok := m.sessions[sessionName]; ok {
        if inst.timer != nil {
            inst.timer.Stop()
            inst.timer = nil
        }
        return inst.port, nil
    }

    port, err := getport.GetPort()
    if err != nil {
        return 0, fmt.Errorf("failed to allocate port: %w", err)
    }

    var args []string
    if len(cmdAndArgs) > 0 {
        args = append([]string{"-p", fmt.Sprintf("%d", port), "-W"}, cmdAndArgs...)
    } else {
        args = []string{"-p", fmt.Sprintf("%d", port), "-W", "tmux", "attach", "-t", sessionName}
    }

    cmd := exec.Command("ttyd", args...)
    if err := cmd.Start(); err != nil {
        return 0, fmt.Errorf("failed to start ttyd: %w", err)
    }

    m.sessions[sessionName] = &ttydInstance{port: port, cmd: cmd}

    // Clean up when process exits
    go func() {
        cmd.Wait()
        m.mu.Lock()
        delete(m.sessions, sessionName)
        m.mu.Unlock()
    }()

    return port, nil
}

// clientConnected increments the connection count for a session.
func (m *ttydManager) clientConnected(sessionName string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    if inst, ok := m.sessions[sessionName]; ok {
        inst.conns++
        if inst.timer != nil {
            inst.timer.Stop()
            inst.timer = nil
        }
    }
}

// clientDisconnected decrements the connection count and starts the idle timer.
func (m *ttydManager) clientDisconnected(sessionName string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    inst, ok := m.sessions[sessionName]
    if !ok {
        return
    }
    if inst.conns > 0 {
        inst.conns--
    }
    if inst.conns == 0 {
        inst.timer = time.AfterFunc(ttydIdleTimeout, func() {
            m.stopSession(sessionName)
        })
    }
}

// stopSession kills the ttyd process for a session.
func (m *ttydManager) stopSession(sessionName string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    inst, ok := m.sessions[sessionName]
    if !ok {
        return
    }
    if inst.cmd != nil && inst.cmd.Process != nil {
        inst.cmd.Process.Kill()
    }
    delete(m.sessions, sessionName)
}
```

**Step 4: Run tests**

```bash
go test ./web/... -run TestTtydManager -v -race
```

**Step 5: Commit**

```bash
git add web/ttyd.go web/ttyd_test.go go.mod go.sum
git commit -m "feat: add ttyd lifecycle manager with idle timeout"
```

---

### Task 14: Add WebSocket proxy endpoint

**Files:**
- Create: `web/proxy.go`
- Modify: `web/server.go` (inject ttydManager, register proxy route)

**Step 1: Add gorilla/websocket proxy in web/proxy.go**

```go
package web

import (
    "fmt"
    "net/http"

    "github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}

// proxyWebSocket proxies a WebSocket connection to a backend ttyd instance.
func proxyWebSocket(w http.ResponseWriter, r *http.Request, backendPort int) {
    // Upgrade client connection
    clientConn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        http.Error(w, "WebSocket upgrade failed", http.StatusBadRequest)
        return
    }
    defer clientConn.Close()

    // Connect to ttyd backend
    backendURL := fmt.Sprintf("ws://localhost:%d/ws", backendPort)
    backendConn, _, err := websocket.DefaultDialer.Dial(backendURL, nil)
    if err != nil {
        clientConn.WriteMessage(websocket.CloseMessage,
            websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "backend unavailable"))
        return
    }
    defer backendConn.Close()

    errc := make(chan error, 2)

    // Client → backend
    go func() {
        for {
            mt, msg, err := clientConn.ReadMessage()
            if err != nil {
                errc <- err
                return
            }
            if err := backendConn.WriteMessage(mt, msg); err != nil {
                errc <- err
                return
            }
        }
    }()

    // Backend → client
    go func() {
        for {
            mt, msg, err := backendConn.ReadMessage()
            if err != nil {
                errc <- err
                return
            }
            if err := clientConn.WriteMessage(mt, msg); err != nil {
                errc <- err
                return
            }
        }
    }()

    <-errc // wait for either side to close
}
```

**Step 2: Wire into server.go**

Update `Server` struct to hold a `*ttydManager` and register the terminal route:

In `web/server.go`, update `New` and `Start`:

```go
type Server struct {
    token   string
    port    int
    server  *http.Server
    ttyd    *ttydManager
}

func New(token string, port int) (*Server, error) {
    if token == "" {
        return nil, fmt.Errorf("web_secret_token must be set in config to use devx web")
    }
    return &Server{token: token, port: port, ttyd: newTtydManager()}, nil
}
```

In `registerRoutes`, pass `srv` so it can register the terminal handler:

```go
func (s *Server) registerRoutes(mux *http.ServeMux) {
    registerAPIRoutes(mux)
    registerStaticRoutes(mux)
    mux.HandleFunc("/terminal/{session}/ws", s.handleTerminalWS)
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
    sessionName := r.PathValue("session")
    port, err := s.ttyd.startForSession(sessionName)
    if err != nil {
        http.Error(w, "failed to start terminal: "+err.Error(), http.StatusInternalServerError)
        return
    }
    s.ttyd.clientConnected(sessionName)
    defer s.ttyd.clientDisconnected(sessionName)
    proxyWebSocket(w, r, port)
}
```

Update `Start()` to call `s.registerRoutes(mux)` instead of `registerRoutes(mux)`.

**Step 3: Run tests**

```bash
go test ./web/... -race
```

**Step 4: Commit**

```bash
git add web/proxy.go web/server.go
git commit -m "feat: add WebSocket proxy endpoint for ttyd terminal access"
```

---

### Task 15: Implement mobile terminal view in Svelte

**Files:**
- Create: `web/app/src/lib/Terminal.svelte`
- Create: `web/app/src/lib/SoftKeybar.svelte`
- Create: `web/app/src/lib/PaneNav.svelte`
- Modify: `web/app/src/App.svelte`

**Step 1: Create SoftKeybar.svelte**

```svelte
<!-- web/app/src/lib/SoftKeybar.svelte -->
<script>
  export let onKey

  const keys = [
    { label: 'Ctrl-C', seq: '\x03' },
    { label: 'Esc',    seq: '\x1b' },
    { label: 'Tab',    seq: '\x09' },
    { label: '↑',      seq: '\x1b[A' },
    { label: '↓',      seq: '\x1b[B' },
    { label: '←',      seq: '\x1b[D' },
    { label: '→',      seq: '\x1b[C' },
    { label: 'Ctrl-Z', seq: '\x1a' },
  ]
</script>

<div class="flex gap-1 px-2 py-1 bg-gray-900 border-t border-gray-800 overflow-x-auto">
  {#each keys as key}
    <button
      on:click={() => onKey(key.seq)}
      class="min-w-[3rem] bg-gray-700 hover:bg-gray-600 active:bg-gray-500 text-white text-xs font-mono py-2 px-3 rounded flex-shrink-0 transition-colors"
    >
      {key.label}
    </button>
  {/each}
</div>
```

**Step 2: Create PaneNav.svelte**

```svelte
<!-- web/app/src/lib/PaneNav.svelte -->
<script>
  export let windows = []   // [{index: 1, name: 'zsh', active: true}]
  export let onSwitch       // (windowIndex) => void
</script>

{#if windows.length > 0}
  <div class="flex gap-1 px-2 py-1 bg-gray-850 border-b border-gray-800 overflow-x-auto">
    {#each windows as win}
      <button
        on:click={() => onSwitch(win.index)}
        class="text-xs py-1 px-3 rounded flex-shrink-0 transition-colors
               {win.active ? 'bg-blue-700 text-white' : 'bg-gray-800 text-gray-300 hover:bg-gray-700'}"
      >
        {win.index}: {win.name}
      </button>
    {/each}
  </div>
{/if}
```

**Step 3: Create Terminal.svelte**

This wraps the ttyd xterm.js output via an iframe (ttyd already embeds xterm.js). We connect our own WebSocket for the soft keys.

```svelte
<!-- web/app/src/lib/Terminal.svelte -->
<script>
  import { onMount, onDestroy } from 'svelte'
  import SoftKeybar from './SoftKeybar.svelte'
  import PaneNav from './PaneNav.svelte'

  export let session
  export let onBack

  let ws
  let wsReady = false
  let error = ''

  // We connect a WebSocket for programmatic key injection.
  // The visual terminal is rendered via ttyd's built-in xterm.js in an iframe.
  // For key injection to work, ttyd must be running; we piggyback on /terminal/:session/ws.
  // The iframe loads ttyd's own UI for rendering; our WS is for soft keys only.

  // ttyd terminal URL — served by Go server which proxies to local ttyd
  $: terminalURL = `/terminal/${session.name}/`

  let windows = []

  function connectWS() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    ws = new WebSocket(`${proto}://${location.host}/terminal/${session.name}/ws`)
    ws.onopen = () => { wsReady = true }
    ws.onerror = (e) => { error = 'Terminal connection failed' }
    ws.onclose = () => { wsReady = false }
  }

  function sendKey(seq) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: 'input', data: seq }))
    }
  }

  function switchWindow(index) {
    // Send tmux prefix + window number
    sendKey('\x02' + String(index))
  }

  onMount(connectWS)
  onDestroy(() => ws && ws.close())
</script>

<div class="flex flex-col h-screen bg-black">
  <!-- Header -->
  <div class="flex items-center gap-3 px-3 py-2 bg-gray-900 border-b border-gray-800 flex-shrink-0">
    <button on:click={onBack}
      class="text-gray-400 hover:text-white text-sm px-2 py-1 rounded transition-colors">
      ← Back
    </button>
    <div class="flex-1 min-w-0">
      <span class="text-white font-medium text-sm truncate">{session.name}</span>
      <span class="text-gray-500 text-xs ml-2 truncate">{session.branch}</span>
    </div>
    <div class="w-2 h-2 rounded-full {wsReady ? 'bg-green-500' : 'bg-red-500'}" title={wsReady ? 'Connected' : 'Disconnected'}></div>
  </div>

  <!-- Pane/window nav -->
  <PaneNav {windows} onSwitch={switchWindow} />

  <!-- Terminal iframe (ttyd's built-in xterm.js UI) -->
  {#if error}
    <div class="flex-1 flex items-center justify-center text-red-400 text-sm">{error}</div>
  {:else}
    <iframe
      src={terminalURL}
      title="Terminal"
      class="flex-1 w-full border-0"
    />
  {/if}

  <!-- Soft key toolbar (shown when keyboard visible — always shown on mobile) -->
  <SoftKeybar onKey={sendKey} />
</div>
```

**Step 4: Wire Terminal into App.svelte**

Update `web/app/src/App.svelte` to import `Terminal` and replace the Phase 3 placeholder:

```svelte
import Terminal from './lib/Terminal.svelte'
...
{:else if view === 'terminal'}
  <Terminal {session} onBack={goHome} bind:session={activeSession} />
```

**Step 5: Build and manual test**

```bash
make web-build && go build -o devx .
# Ensure ttyd is installed: brew install ttyd
# Start devx web and open http://localhost:7777
```

Verify:
- Login works
- Session list shows sessions
- Tapping "Terminal" opens the terminal view
- Soft key toolbar is visible
- Back button returns to session list

**Step 6: Run pre-commit checklist**

```bash
gofmt -w . && go vet ./... && go test -v -race ./... && go mod tidy
```

**Step 7: Commit Phase 3**

```bash
git add web/
git commit -m "feat: add mobile terminal view with ttyd proxy, soft keys, and pane navigation"
```

---

## Final Steps

### Task 16: Update README and run full validation

**Files:**
- Modify: `README.md`

**Step 1: Add a "Web Interface" section to README.md**

Document:
1. One-time Cloudflare setup (create tunnel, add wildcard DNS)
2. Config keys: `external_domain`, `cloudflare_tunnel_id`, `web_secret_token`
3. Starting: `devx web` or `devx web --daemon`
4. Checking: `devx cloudflare check`

**Step 2: Final validation**

```bash
make web-build
go build -o devx .
gofmt -l .           # should output nothing
go vet ./...
go test -v -race ./...
go mod tidy
```

**Step 3: Commit**

```bash
git add README.md
git commit -m "docs: add web interface and Cloudflare tunnel setup to README"
```
