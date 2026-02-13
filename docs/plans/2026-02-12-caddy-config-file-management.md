# Caddy Config File Management — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace API-based Caddy route management with config file generation + `caddy reload` so routes survive restarts and ordering bugs are eliminated.

**Architecture:** A new `SyncRoutes()` function builds a complete Caddy JSON config from `sessions.json`, writes it atomically to `~/.config/devx/caddy-config.json`, and runs `caddy reload`. All per-route API calls (`CreateRoute`, `DeleteRoute`, `reorderRoutes`, etc.) are deleted. The `CaddyClient` is kept only for health checks (`CheckCaddyConnection`, `GetAllRoutes`).

**Tech Stack:** Go, Caddy JSON config format, `os/exec` for `caddy reload`

**Design doc:** `docs/plans/2026-02-12-caddy-config-file-management-design.md`

---

### Task 1: Create `caddy/config.go` with `BuildCaddyConfig` and `SyncRoutes`

This is the core new file. It builds the full Caddy JSON config and writes it atomically.

**Files:**
- Create: `caddy/config.go`
- Create: `caddy/config_test.go`

**Step 1: Write failing tests for `BuildCaddyConfig`**

Create `caddy/config_test.go`:

```go
package caddy

import (
	"encoding/json"
	"testing"
)

func TestBuildCaddyConfig(t *testing.T) {
	// Ensure clean Viper state for all subtests
	viper.Set("caddy_admin", "")

	t.Run("empty sessions produces valid config with no routes", func(t *testing.T) {
		sessions := map[string]*SessionInfo{}
		config := BuildCaddyConfig(sessions)

		jsonData, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal config: %v", err)
		}

		jsonStr := string(jsonData)
		// Should have admin listener
		if !contains(jsonStr, `"listen":"localhost:2019"`) {
			t.Errorf("missing admin listener in config: %s", jsonStr)
		}
		// Should have server listening on :80
		if !contains(jsonStr, `":80"`) {
			t.Errorf("missing :80 listener in config: %s", jsonStr)
		}
		// Routes should be empty array, not null
		if !contains(jsonStr, `"routes":[]`) {
			t.Errorf("expected empty routes array in config: %s", jsonStr)
		}
	})

	t.Run("single session produces correct routes", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"my-session": {
				Name:  "my-session",
				Ports: map[string]int{"FRONTEND": 3000, "BACKEND": 4000},
			},
		}
		config := BuildCaddyConfig(sessions)

		jsonData, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal config: %v", err)
		}

		jsonStr := string(jsonData)
		if !contains(jsonStr, `my-session-frontend.localhost`) {
			t.Errorf("missing frontend hostname: %s", jsonStr)
		}
		if !contains(jsonStr, `my-session-backend.localhost`) {
			t.Errorf("missing backend hostname: %s", jsonStr)
		}
		if !contains(jsonStr, `localhost:3000`) {
			t.Errorf("missing frontend port: %s", jsonStr)
		}
		if !contains(jsonStr, `localhost:4000`) {
			t.Errorf("missing backend port: %s", jsonStr)
		}
	})

	t.Run("session with project alias includes prefix", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"my-session": {
				Name:         "my-session",
				Ports:        map[string]int{"FRONTEND": 3000},
				ProjectAlias: "myproject",
			},
		}
		config := BuildCaddyConfig(sessions)

		jsonData, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("failed to marshal config: %v", err)
		}

		jsonStr := string(jsonData)
		if !contains(jsonStr, `myproject-my-session-frontend.localhost`) {
			t.Errorf("missing project-prefixed hostname: %s", jsonStr)
		}
		if !contains(jsonStr, `sess-myproject-my-session-frontend`) {
			t.Errorf("missing project-prefixed route ID: %s", jsonStr)
		}
	})

	t.Run("route IDs and hostnames are deterministically ordered", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"b-session": {
				Name:  "b-session",
				Ports: map[string]int{"UI": 3000},
			},
			"a-session": {
				Name:  "a-session",
				Ports: map[string]int{"UI": 4000},
			},
		}
		config1 := BuildCaddyConfig(sessions)
		config2 := BuildCaddyConfig(sessions)

		json1, _ := json.Marshal(config1)
		json2, _ := json.Marshal(config2)

		if string(json1) != string(json2) {
			t.Errorf("config generation is not deterministic")
		}
	})

	t.Run("session with slashes in name is sanitized", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"feature/my-branch": {
				Name:  "feature/my-branch",
				Ports: map[string]int{"FRONTEND": 3000},
			},
		}
		config := BuildCaddyConfig(sessions)

		jsonData, _ := json.Marshal(config)
		jsonStr := string(jsonData)
		// Slashes should be converted to hyphens
		if !contains(jsonStr, `feature-my-branch-frontend.localhost`) {
			t.Errorf("session name with slash not properly sanitized: %s", jsonStr)
		}
	})

	t.Run("session with empty ports produces no routes", func(t *testing.T) {
		sessions := map[string]*SessionInfo{
			"empty": {
				Name:  "empty",
				Ports: map[string]int{},
			},
		}
		config := BuildCaddyConfig(sessions)

		routes := config.Apps.HTTP.Servers["devx"].Routes
		if len(routes) != 0 {
			t.Errorf("expected 0 routes for empty ports, got %d", len(routes))
		}
	})
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./caddy/ -run TestBuildCaddyConfig -v`
Expected: FAIL — `BuildCaddyConfig` not defined

**Step 3: Implement `BuildCaddyConfig` and `SyncRoutes`**

Create `caddy/config.go`:

```go
package caddy

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

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
		sanitizedSession := SanitizeHostname(sessionName)

		// Sort service names for deterministic output
		serviceNames := make([]string, 0, len(info.Ports))
		for svc := range info.Ports {
			serviceNames = append(serviceNames, svc)
		}
		sort.Strings(serviceNames)

		for _, serviceName := range serviceNames {
			port := info.Ports[serviceName]
			dnsService := NormalizeDNSName(serviceName)
			if dnsService == "" {
				continue
			}

			hostname := fmt.Sprintf("%s-%s.localhost", sanitizedSession, dnsService)
			routeID := fmt.Sprintf("sess-%s-%s", sanitizedSession, dnsService)
			if info.ProjectAlias != "" {
				hostname = fmt.Sprintf("%s-%s-%s.localhost", info.ProjectAlias, sanitizedSession, dnsService)
				routeID = fmt.Sprintf("sess-%s-%s-%s", info.ProjectAlias, sanitizedSession, dnsService)
			}

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
```

**Step 4: Run tests to verify they pass**

Run: `go test ./caddy/ -run TestBuildCaddyConfig -v`
Expected: All 4 tests PASS

**Step 5: Commit**

```bash
git add caddy/config.go caddy/config_test.go
git commit -m "feat: add Caddy config file generation with SyncRoutes"
```

---

### Task 2: Write `SyncRoutes` tests

Test the full `SyncRoutes` flow: config writing, atomic write behavior, and `disable_caddy` flag.

**Files:**
- Modify: `caddy/config_test.go`

**Step 1: Add `SyncRoutes` tests**

Append to `caddy/config_test.go`:

```go
func TestSyncRoutes(t *testing.T) {
	t.Run("writes config file", func(t *testing.T) {
		// Use a temp dir to avoid writing to real config
		tmpDir := t.TempDir()
		t.Setenv("HOME", tmpDir)

		// Create the config directory
		configDir := filepath.Join(tmpDir, ".config", "devx")
		if err := os.MkdirAll(configDir, 0755); err != nil {
			t.Fatalf("failed to create config dir: %v", err)
		}

		sessions := map[string]*SessionInfo{
			"test-session": {
				Name:  "test-session",
				Ports: map[string]int{"FRONTEND": 3000},
			},
		}

		err := SyncRoutes(sessions)
		// SyncRoutes may warn about caddy reload failing, that's OK
		if err != nil {
			t.Fatalf("SyncRoutes failed: %v", err)
		}

		// Verify config file was written
		cfgFile := filepath.Join(configDir, "caddy-config.json")
		data, err := os.ReadFile(cfgFile)
		if err != nil {
			t.Fatalf("config file not written: %v", err)
		}

		// Verify it's valid JSON with expected content
		var config CaddyConfig
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatalf("config file is not valid JSON: %v", err)
		}

		if len(config.Apps.HTTP.Servers["devx"].Routes) != 1 {
			t.Errorf("expected 1 route, got %d", len(config.Apps.HTTP.Servers["devx"].Routes))
		}
	})

	t.Run("skips when disable_caddy is true", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("HOME", tmpDir)

		viper.Set("disable_caddy", true)
		defer viper.Set("disable_caddy", false)

		sessions := map[string]*SessionInfo{
			"test": {Name: "test", Ports: map[string]int{"UI": 3000}},
		}

		err := SyncRoutes(sessions)
		if err != nil {
			t.Fatalf("SyncRoutes should not error when disabled: %v", err)
		}

		// Config file should NOT exist
		cfgFile := filepath.Join(tmpDir, ".config", "devx", "caddy-config.json")
		if _, err := os.Stat(cfgFile); !os.IsNotExist(err) {
			t.Error("config file should not be written when caddy is disabled")
		}
	})
}
```

Add these imports to the test file's import block: `"os"`, `"path/filepath"`, `"github.com/spf13/viper"`.

**Step 2: Run tests**

Run: `go test ./caddy/ -run TestSyncRoutes -v`
Expected: PASS (the `caddy reload` will fail in tests but `SyncRoutes` handles that gracefully)

**Step 3: Commit**

```bash
git add caddy/config_test.go
git commit -m "test: add SyncRoutes tests for config file writing"
```

---

### Task 3: Update `cmd/session_create.go` to use `SyncRoutes`

Replace `ProvisionSessionRoutesWithProject` call with `SyncRoutes`.

**Files:**
- Modify: `cmd/session_create.go:219-270`

**Step 1: Replace the Caddy provisioning block**

In `cmd/session_create.go`, replace lines 219-270 (the entire Caddy provisioning + hostname generation + route update block) with:

```go
	// Build hostname map for environment variables
	hostnames := make(map[string]string)
	for serviceName := range portAllocation.Ports {
		dnsServiceName := caddy.NormalizeDNSName(serviceName)
		sanitizedSessionName := caddy.SanitizeHostname(name)
		if projectAlias != "" {
			hostnames[serviceName] = fmt.Sprintf("%s-%s-%s.localhost", projectAlias, sanitizedSessionName, dnsServiceName)
		} else {
			hostnames[serviceName] = fmt.Sprintf("%s-%s.localhost", sanitizedSessionName, dnsServiceName)
		}
	}

	// Generate .envrc file
	envData := session.EnvrcData{
		Ports:  portAllocation.Ports,
		Routes: hostnames,
		Name:   name,
	}
	if err := session.GenerateEnvrc(worktreePath, envData); err != nil {
		return fmt.Errorf("failed to generate .envrc: %w", err)
	}

	// Generate tmuxp config
	tmuxpData := session.TmuxpData{
		Name:   name,
		Path:   worktreePath,
		Ports:  portAllocation.Ports,
		Routes: hostnames,
	}
	if err := session.GenerateTmuxpConfig(worktreePath, tmuxpData); err != nil {
		return fmt.Errorf("failed to generate tmuxp config: %w", err)
	}

	// Sync all Caddy routes (writes config file + reloads)
	if err := syncAllCaddyRoutes(); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}
```

This removes the `ProvisionSessionRoutesWithProject` call and the route-to-hostname conversion (hostnames are now computed directly).

**IMPORTANT:** Keep the `store.UpdateSession` block (lines 262-270) but change it to save **hostnames** instead of route IDs. `sess.Routes` is read by cleanup.go (HOST env vars), TUI (openRoutes, loadHostnames), and session_list.go. Replace:

```go
	// Update session with route information
	if len(hostnames) > 0 {
		if err := store.UpdateSession(name, func(s *session.Session) {
			s.Routes = hostnames
		}); err != nil {
			fmt.Printf("Warning: failed to update session routes: %v\n", err)
		}
	}
```

**Step 2: Add the `syncAllCaddyRoutes` helper function**

Add to `cmd/session_create.go` (or a shared location like `cmd/caddy_helpers.go` — but since it's also needed in `session_rm.go`, put it in a new file `cmd/caddy_sync.go`):

Create `cmd/caddy_sync.go`:

```go
package cmd

import (
	"fmt"

	"github.com/jfox85/devx/caddy"
	"github.com/jfox85/devx/config"
	"github.com/jfox85/devx/session"
)

// syncAllCaddyRoutes loads all sessions and syncs Caddy routes.
// This is called after session create and session remove.
func syncAllCaddyRoutes() error {
	store, err := session.LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions for Caddy sync: %w", err)
	}

	registry, err := config.LoadProjectRegistry()
	if err != nil {
		return fmt.Errorf("failed to load project registry: %w", err)
	}

	sessionInfos := make(map[string]*caddy.SessionInfo)
	for name, sess := range store.Sessions {
		info := &caddy.SessionInfo{
			Name:  name,
			Ports: sess.Ports,
		}

		for alias, project := range registry.Projects {
			if sess.ProjectPath == project.Path {
				info.ProjectAlias = alias
				break
			}
		}

		sessionInfos[name] = info
	}

	return caddy.SyncRoutes(sessionInfos)
}
```

**Step 3: Verify imports**

After the edit, `session_create.go` still uses `caddy.NormalizeDNSName` and `caddy.SanitizeHostname`, so the caddy import stays.

**Step 4: Run tests**

Run: `go build ./... && go test ./cmd/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add cmd/session_create.go cmd/caddy_sync.go
git commit -m "refactor: session create uses SyncRoutes instead of API provisioning"
```

---

### Task 4: Update `cmd/session_rm.go` to use `SyncRoutes`

Replace `DestroySessionRoutes` call with `SyncRoutes`.

**Files:**
- Modify: `cmd/session_rm.go:69-74`

**Step 1: Replace the Caddy route removal block**

Replace lines 69-74:

```go
	// Remove Caddy routes
	if len(sess.Routes) > 0 {
		if err := caddy.DestroySessionRoutes(name, sess.Routes); err != nil {
			fmt.Printf("Warning: failed to remove Caddy routes: %v\n", err)
		}
	}
```

With:

```go
	// Sync Caddy routes (session already removed from store below,
	// so we sync after RemoveSession to regenerate without this session)
```

Then, AFTER the `store.RemoveSession(name)` call (line 87), add:

```go
	// Sync Caddy routes after removal
	if err := syncAllCaddyRoutes(); err != nil {
		fmt.Printf("Warning: failed to sync Caddy routes: %v\n", err)
	}
```

**Step 2: Remove the `caddy` import from `session_rm.go`**

The `caddy` import is no longer used directly — `syncAllCaddyRoutes` lives in the same `cmd` package.

**Step 3: Run tests**

Run: `go build ./... && go test ./cmd/ -v`
Expected: PASS

**Step 4: Commit**

```bash
git add cmd/session_rm.go
git commit -m "refactor: session rm uses SyncRoutes instead of API deletion"
```

---

### Task 5: Rewrite `cmd/caddy.go` health check and fix

Simplify the health check to compare expected config vs running config, and replace `RepairRoutes` with `SyncRoutes`.

**Files:**
- Modify: `cmd/caddy.go`

**Step 1: Rewrite `runCaddyCheck`**

Replace the `runCaddyCheck` function body. The new flow:
1. Load sessions + build `sessionInfos` (same as before)
2. Call `caddy.CheckCaddyHealth(sessionInfos)` (kept, but simplified)
3. Display results (simplified — no more "Blocked" status)
4. If `--fix`, call `syncAllCaddyRoutes()` then re-check

Replace lines 79-93 (the fix block):

```go
	// Fix issues if requested
	if fixFlag {
		fmt.Println("\nSyncing Caddy config...")
		if err := syncAllCaddyRoutes(); err != nil {
			return fmt.Errorf("failed to sync routes: %w", err)
		}

		// Re-run health check to show updated status
		fmt.Println("\nRechecking after sync...")
		result, err = caddy.CheckCaddyHealth(sessionInfos)
		if err != nil {
			return fmt.Errorf("failed to recheck Caddy health: %w", err)
		}
		displayHealthCheckResults(result)
	}
```

**Step 2: Simplify `displayHealthCheckResults`**

Remove the `CatchAllFirst` warning and the "Blocked" status logic. Replace the status text block (lines 131-138):

```go
		for _, status := range result.RouteStatuses {
			statusText := "✗ Missing"
			if status.Exists {
				statusText = "✓ Active"
			}
```

Remove the `CatchAllFirst` warning block (lines 115-121) and the condition on line 165 that checks `CatchAllFirst`.

**Step 3: Run tests**

Run: `go build ./...`
Expected: Compiles cleanly

**Step 4: Commit**

```bash
git add cmd/caddy.go
git commit -m "refactor: caddy check uses SyncRoutes for --fix, remove Blocked status"
```

---

### Task 6: Simplify `caddy/health.go`

Remove `RepairRoutes`, `CatchAllFirst`, `IsFirst`, and route ordering logic from the health check. Keep `CheckCaddyHealth` but simplify it.

**Files:**
- Modify: `caddy/health.go`

**Step 1: Simplify `RouteStatus` and `HealthCheckResult`**

Remove `IsFirst` from `RouteStatus`. Remove `CatchAllFirst` from `HealthCheckResult`.

```go
type RouteStatus struct {
	SessionName string
	ServiceName string
	RouteID     string
	Hostname    string
	Port        int
	Exists      bool
	ServiceUp   bool
	Error       string
}

type HealthCheckResult struct {
	CaddyRunning   bool
	CaddyError     string
	RouteStatuses  []RouteStatus
	RoutesNeeded   int
	RoutesExisting int
	RoutesWorking  int
}
```

**Step 2: Simplify `CheckCaddyHealth`**

Remove the catch-all detection logic (lines 55-70) and the `IsFirst` assignment (line 112). The route existence check (lines 110-121) simplifies to:

```go
		if _, exists := existingRoutes[routeID]; exists {
			status.Exists = true
			result.RoutesExisting++
			result.RoutesWorking++
		}
```

**Step 3: Delete `RepairRoutes` function entirely** (lines 138-198)

It's replaced by `SyncRoutes`.

**Step 4: Run tests**

Run: `go test ./caddy/ -v && go build ./...`
Expected: PASS (some tests for deleted functions will need removal — see Task 7)

**Step 5: Commit**

```bash
git add caddy/health.go
git commit -m "refactor: simplify health check, remove RepairRoutes and route ordering logic"
```

---

### Task 7: Clean up `caddy/routes.go` — delete unused API functions

Remove functions that are no longer called.

**Files:**
- Modify: `caddy/routes.go`
- Modify: `caddy/routes_test.go`

**Step 1: Delete these functions from `caddy/routes.go`:**

- `discoverServerName` (lines 70-97) — server name is always `devx` now
- `CreateRoute` (lines 105-107)
- `CreateRouteWithProject` (lines 110-171)
- `DeleteRoute` (lines 174-187)
- `DeleteSessionRoutes` (lines 190-221)
- `ReplaceAllRoutes` (lines 328-359)
- `EnsureRoutesArray` (lines 257-289)
- `GetServiceMapping` (lines 306-325) — unused

**Step 2: Simplify `NewCaddyClient`**

Remove the `discoverServerName()` call. Hardcode `serverName` to `"devx"`:

```go
func NewCaddyClient() *CaddyClient {
	caddyAPI := viper.GetString("caddy_api")
	if caddyAPI == "" {
		caddyAPI = "http://localhost:2019"
	}

	client := resty.New()
	client.SetTimeout(10 * time.Second)

	return &CaddyClient{
		client:     client,
		baseURL:    caddyAPI,
		serverName: "devx",
	}
}
```

**Step 3: Delete these tests from `caddy/routes_test.go`:**

- `TestDiscoverServerName` (lines 179-255)
- `TestEnsureRoutesArray` (lines 259-328)
- `TestServerPath` (lines 386-392)
- `TestRoutesUseDiscoveredServer` (lines 396-451)
- `TestGetServiceMapping` (lines 58-83)

Keep: `TestRouteGeneration`, `TestSanitizeHostname`, `TestNormalizeDNSName`, `TestGetAllRoutesNullResponse`, and `newTestClient` helper.

**Step 4: Run tests**

Run: `go test ./caddy/ -v && go build ./...`
Expected: All PASS

**Step 5: Commit**

```bash
git add caddy/routes.go caddy/routes_test.go
git commit -m "refactor: remove API-based route management functions"
```

---

### Task 8: Delete `caddy/provisioning.go`

The entire file is replaced by `SyncRoutes` in `config.go`.

**Files:**
- Delete: `caddy/provisioning.go`

**Step 1: Verify no remaining references**

Run: `grep -r "ProvisionSession\|DestroySession\|reorderRoutes" --include="*.go" .` (excluding `.worktrees/`)

Expected: No matches in non-worktree code.

**Step 2: Delete the file**

```bash
rm caddy/provisioning.go
```

**Step 3: Move `NormalizeDNSName` and `SanitizeHostname` if they're defined in provisioning.go**

These are actually defined in `caddy/provisioning.go` (lines 11-71). Move them to a surviving file. The cleanest place is a new `caddy/hostname.go` or just into `caddy/config.go`. Since they're utility functions used by config generation, add them to `caddy/config.go` (before `BuildCaddyConfig`).

**Step 4: Run tests**

Run: `go test ./... -v && go build ./...`
Expected: PASS

**Step 5: Commit**

```bash
git add caddy/provisioning.go caddy/config.go
git commit -m "refactor: delete provisioning.go, move hostname utils to config.go"
```

---

### Task 9: Update `session/metadata.go` — remove `removeCaddyRoutes` and fix `RemoveSession`

**Files:**
- Modify: `session/metadata.go:165-182, 228-231`

**Step 1: Delete the `removeCaddyRoutes` function** (lines 228-231)

```go
func removeCaddyRoutes(sessionName string, routes map[string]string) error {
	return caddy.DestroySessionRoutes(sessionName, routes)
}
```

**Step 2: Remove the Caddy route removal from `RemoveSession`** (line 173-176)

In `RemoveSession()`, delete:

```go
	// Remove Caddy routes
	if len(sess.Routes) > 0 {
		_ = removeCaddyRoutes(name, sess.Routes) // Don't fail on Caddy errors
	}
```

Note: Caddy route cleanup is now handled by the caller via `syncAllCaddyRoutes()` after all sessions are removed from the store.

**Step 3: Remove the `caddy` import**

The import `"github.com/jfox85/devx/caddy"` should now be unused — remove it.

**Step 4: Run tests**

Run: `go build ./... && go test ./session/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add session/metadata.go
git commit -m "refactor: remove Caddy API calls from session metadata"
```

---

### Task 10: Fix `session/cleanup.go` — remove caddy import dependency

The cleanup environment builder at `cleanup.go:54-67` iterates `sess.Routes` to derive HOST env vars. After Task 3, `sess.Routes` now stores hostnames directly (e.g., `"toneclone-jf-add-mcp-frontend.localhost"`), so we can use them directly instead of reconstructing from the session name.

**Files:**
- Modify: `session/cleanup.go:53-67`

**Step 1: Simplify the hostname generation**

Replace lines 53-67:

```go
	// Add hostname variables if routes exist
	if len(sess.Routes) > 0 {
		for serviceName := range sess.Routes {
			// Convert service name to HOST variable name
			// e.g., "ui" -> "UI_HOST", "auth-service" -> "AUTH_SERVICE_HOST"
			hostVar := strings.ToUpper(serviceName)
			hostVar = strings.ReplaceAll(hostVar, "-", "_") + "_HOST"

			// Reconstruct the hostname from the route ID
			// Route IDs are typically in format: "session-service.localhost"
			// Sanitize session name for hostname compatibility
			sanitizedSessionName := caddy.SanitizeHostname(sess.Name)
			hostname := fmt.Sprintf("https://%s-%s.localhost", sanitizedSessionName, strings.ToLower(serviceName))
			env = append(env, fmt.Sprintf("%s=%s", hostVar, hostname))
		}
	}
```

With:

```go
	// Add hostname variables from stored routes
	for serviceName, hostname := range sess.Routes {
		hostVar := strings.ToUpper(serviceName)
		hostVar = strings.ReplaceAll(hostVar, "-", "_") + "_HOST"
		env = append(env, fmt.Sprintf("%s=http://%s", hostVar, hostname))
	}
```

**Step 2: Remove the `caddy` import**

The `caddy.SanitizeHostname` call is gone, so remove `"github.com/jfox85/devx/caddy"` from imports.

**Step 3: Run tests**

Run: `go build ./... && go test ./session/ -v`
Expected: PASS

**Step 4: Commit**

```bash
git add session/cleanup.go
git commit -m "refactor: cleanup uses stored hostnames instead of reconstructing them"
```

---

### Task 11: Fix TUI `openRoutes` — use stored hostnames

The `openRoutes` function at `tui/model.go:1917` does `http://%s.localhost` with the stored value, which previously produced broken URLs since route IDs were stored. Now that `sess.Routes` stores hostnames, fix the URL construction.

**Files:**
- Modify: `tui/model.go:1917-1918`

**Step 1: Fix URL construction in `openRoutes`**

Replace line 1917-1918:

```go
		for _, hostname := range sess.Routes {
			url := fmt.Sprintf("http://%s.localhost", hostname)
```

With:

```go
		for _, hostname := range sess.Routes {
			url := fmt.Sprintf("http://%s", hostname)
```

The hostname already includes `.localhost` (e.g., `"toneclone-jf-add-mcp-frontend.localhost"`).

**Step 2: Run build**

Run: `go build ./...`
Expected: Compiles cleanly

**Step 3: Commit**

```bash
git add tui/model.go
git commit -m "fix: openRoutes uses stored hostname directly instead of appending .localhost"
```

---

### Task 12: Update TUI health check and Caddy help message

Simplify the TUI's Caddy health warning to remove `CatchAllFirst` references. Fix stale Caddyfile path in help message.

**Files:**
- Modify: `tui/model.go:1618-1627`
- Modify: `cmd/caddy.go:105`

**Step 1: Simplify the warning logic in TUI**

Replace lines 1618-1627:

```go
		// Generate warning message if issues found
		var warning string
		if !result.CaddyRunning {
			warning = "Caddy is not running. Session hostnames won't work."
		} else if result.RoutesNeeded > result.RoutesExisting {
			missing := result.RoutesNeeded - result.RoutesExisting
			warning = fmt.Sprintf("%d Caddy routes are missing. Run 'devx caddy check --fix' to repair.", missing)
		}
```

This removes the `CatchAllFirst` check since that field no longer exists.

**Step 2: Fix stale Caddyfile path in `cmd/caddy.go`**

At line 105, replace:

```go
		fmt.Println("  caddy run --config ~/.config/devx/Caddyfile")
```

With:

```go
		fmt.Println("  caddy run --config ~/.config/devx/caddy-config.json")
		fmt.Println("  (Run 'devx caddy check --fix' first to generate the config file)")
```

**Step 3: Run build**

Run: `go build ./...`
Expected: Compiles cleanly

**Step 4: Commit**

```bash
git add tui/model.go cmd/caddy.go
git commit -m "refactor: simplify TUI health warning, fix Caddy config path in help message"
```

---

### Task 13: Update integration test

Rewrite the Caddy integration test to test `SyncRoutes` instead of the old API-based flow.

**Files:**
- Modify: `caddy/integration_test.go`

**Step 1: Rewrite the integration test**

Replace the full file contents. The new test:
1. Checks if Caddy is available (skip if not)
2. Calls `SyncRoutes` with test sessions
3. Verifies routes exist in Caddy via `GetAllRoutes`
4. Calls `SyncRoutes` with empty sessions to clean up
5. Verifies routes are gone

```go
package caddy

import (
	"net/http"
	"testing"
)

func TestCaddyIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewCaddyClient()

	// Check if Caddy is running
	caddyResp, err := http.Get("http://localhost:2019/config/")
	if err != nil {
		t.Skipf("Caddy not available (connection failed): %v", err)
	}
	defer caddyResp.Body.Close()

	if caddyResp.StatusCode != http.StatusOK {
		t.Skipf("Caddy not available (status %d)", caddyResp.StatusCode)
	}

	// Create test sessions
	sessions := map[string]*SessionInfo{
		"integration-test": {
			Name:  "integration-test",
			Ports: map[string]int{"ui": 18080, "api": 18081},
		},
	}

	// Sync routes
	err = SyncRoutes(sessions)
	if err != nil {
		t.Fatalf("SyncRoutes failed: %v", err)
	}

	// Verify routes exist
	routes, err := client.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes failed: %v", err)
	}

	foundUI := false
	foundAPI := false
	for _, route := range routes {
		if route.ID == "sess-integration-test-ui" {
			foundUI = true
		}
		if route.ID == "sess-integration-test-api" {
			foundAPI = true
		}
	}

	if !foundUI {
		t.Error("expected ui route to exist")
	}
	if !foundAPI {
		t.Error("expected api route to exist")
	}

	// Clean up by syncing empty sessions
	err = SyncRoutes(map[string]*SessionInfo{})
	if err != nil {
		t.Fatalf("cleanup SyncRoutes failed: %v", err)
	}

	// Verify routes are gone
	routes, err = client.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes after cleanup failed: %v", err)
	}

	for _, route := range routes {
		if route.ID == "sess-integration-test-ui" || route.ID == "sess-integration-test-api" {
			t.Errorf("route %s should have been removed", route.ID)
		}
	}
}
```

**Step 2: Run integration test (only if Caddy is running)**

Run: `go test ./caddy/ -run TestCaddyIntegration -v`
Expected: PASS (or skip if Caddy not available)

**Step 3: Commit**

```bash
git add caddy/integration_test.go
git commit -m "test: rewrite integration test for SyncRoutes"
```

---

### Task 14: Full test suite + manual verification

Run all tests, build, and manually verify with a real `devx caddy check`.

**Files:** None (verification only)

**Step 1: Run full test suite**

```bash
gofmt -w .
go vet ./...
go test -v -race ./...
go mod tidy
```

Expected: All PASS

**Step 2: Build and run manual check**

```bash
make build
./devx caddy check
```

Expected: Shows all routes as "Active". No "Blocked" status.

**Step 3: Test `--fix`**

```bash
./devx caddy check --fix
```

Expected: Writes config, reloads Caddy, shows all routes Active.

**Step 4: Verify config file exists**

```bash
cat ~/.config/devx/caddy-config.json | python3 -m json.tool | head -20
```

Expected: Valid JSON with admin config and session routes.

**Step 5: Commit any final fixes**

```bash
git add -A
git commit -m "chore: final cleanup for Caddy config file management"
```
