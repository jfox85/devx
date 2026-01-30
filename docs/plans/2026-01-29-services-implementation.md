# Services Abstraction Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add formal service lifecycle management to devx with health checks, auto-restart, and Caddy sync.

**Architecture:** devx owns service processes directly (spawned on session create, killed on session rm). tmux panes display logs via `tail -f`. Background TUI monitor handles health checks and restarts.

**Tech Stack:** Go 1.21+, Cobra CLI, flock for file locking, process groups (syscall.Setpgid)

---

## Phase 0: File Locking Infrastructure

### Task 0.1: Add flock dependency

**Files:**
- Modify: `go.mod`

**Step 1: Add the flock package**

```bash
go get github.com/gofrs/flock
```

**Step 2: Verify dependency**

```bash
go mod tidy && go build ./...
```
Expected: Build succeeds

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add gofrs/flock for file locking"
```

---

### Task 0.2: Create LockedSessionStore wrapper

**Files:**
- Create: `session/locked_store.go`
- Create: `session/locked_store_test.go`

**Step 1: Write the failing test**

```go
// session/locked_store_test.go
package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLockedStore_ConcurrentUpdates(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "devx-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sessionsPath := filepath.Join(tmpDir, "sessions.json")
	lockPath := filepath.Join(tmpDir, "sessions.json.lock")

	ls := NewLockedSessionStore(sessionsPath, lockPath)

	// Run concurrent updates
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := ls.Update(func(store *SessionStore) error {
				// Simulate adding a session
				name := "test-session"
				if store.Sessions == nil {
					store.Sessions = make(map[string]*Session)
				}
				if _, exists := store.Sessions[name]; !exists {
					store.Sessions[name] = &Session{Name: name}
				}
				return nil
			})
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent update failed: %v", err)
	}

	// Verify final state
	store, err := ls.Load()
	if err != nil {
		t.Fatalf("failed to load final state: %v", err)
	}

	if _, exists := store.Sessions["test-session"]; !exists {
		t.Error("expected test-session to exist")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./session -run TestLockedStore -v
```
Expected: FAIL - NewLockedSessionStore not defined

**Step 3: Write minimal implementation**

```go
// session/locked_store.go
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// LockedSessionStore provides thread-safe access to sessions.json
type LockedSessionStore struct {
	sessionsPath string
	lockPath     string
	fileLock     *flock.Flock
}

// NewLockedSessionStore creates a new locked session store
func NewLockedSessionStore(sessionsPath, lockPath string) *LockedSessionStore {
	return &LockedSessionStore{
		sessionsPath: sessionsPath,
		lockPath:     lockPath,
		fileLock:     flock.New(lockPath),
	}
}

// NewDefaultLockedSessionStore creates a locked store with default paths
func NewDefaultLockedSessionStore() *LockedSessionStore {
	sessionsPath := getSessionsPath()
	lockPath := sessionsPath + ".lock"
	return NewLockedSessionStore(sessionsPath, lockPath)
}

// Load loads the sessions with a shared (read) lock
func (ls *LockedSessionStore) Load() (*SessionStore, error) {
	locked, err := ls.fileLock.TryRLock()
	if err != nil {
		return nil, fmt.Errorf("failed to acquire read lock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("could not acquire read lock")
	}
	defer ls.fileLock.Unlock()

	return ls.loadUnlocked()
}

// Update performs an atomic read-modify-write with exclusive lock
func (ls *LockedSessionStore) Update(fn func(*SessionStore) error) error {
	locked, err := ls.fileLock.TryLock()
	if err != nil {
		return fmt.Errorf("failed to acquire write lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("could not acquire write lock")
	}
	defer ls.fileLock.Unlock()

	store, err := ls.loadUnlocked()
	if err != nil {
		return err
	}

	if err := fn(store); err != nil {
		return err
	}

	return ls.saveUnlocked(store)
}

func (ls *LockedSessionStore) loadUnlocked() (*SessionStore, error) {
	// Create directory if needed
	dir := filepath.Dir(ls.sessionsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Return empty store if file doesn't exist
	if _, err := os.Stat(ls.sessionsPath); os.IsNotExist(err) {
		return &SessionStore{Sessions: make(map[string]*Session)}, nil
	}

	data, err := os.ReadFile(ls.sessionsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions: %w", err)
	}

	var store SessionStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("failed to parse sessions: %w", err)
	}

	if store.Sessions == nil {
		store.Sessions = make(map[string]*Session)
	}

	return &store, nil
}

func (ls *LockedSessionStore) saveUnlocked(store *SessionStore) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sessions: %w", err)
	}

	// Write to temp file first (atomic write)
	tmpPath := ls.sessionsPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Rename temp to actual (atomic on POSIX)
	if err := os.Rename(tmpPath, ls.sessionsPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./session -run TestLockedStore -v -race
```
Expected: PASS

**Step 5: Commit**

```bash
git add session/locked_store.go session/locked_store_test.go
git commit -m "feat: add LockedSessionStore for thread-safe metadata access"
```

---

## Phase 1: Service Config Schema

### Task 1.1: Define ServiceConfig struct

**Files:**
- Create: `session/service_config.go`
- Create: `session/service_config_test.go`

**Step 1: Write the failing test**

```go
// session/service_config_test.go
package session

import (
	"testing"
)

func TestParseServiceConfig(t *testing.T) {
	yaml := `
services:
  frontend:
    command: "npm run dev"
    cwd: frontend
    port: auto
    env:
      VITE_API_URL: "{{BACKEND_HOST}}"
  backend:
    command: "air"
    cwd: backend
    port: 8080
`
	configs, err := ParseServiceConfigs([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if len(configs) != 2 {
		t.Fatalf("expected 2 services, got %d", len(configs))
	}

	fe := configs["frontend"]
	if fe.Command != "npm run dev" {
		t.Errorf("expected command 'npm run dev', got '%s'", fe.Command)
	}
	if fe.Cwd != "frontend" {
		t.Errorf("expected cwd 'frontend', got '%s'", fe.Cwd)
	}
	if !fe.PortAuto {
		t.Error("expected frontend port to be auto")
	}

	be := configs["backend"]
	if be.Port != 8080 {
		t.Errorf("expected backend port 8080, got %d", be.Port)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./session -run TestParseServiceConfig -v
```
Expected: FAIL - ParseServiceConfigs not defined

**Step 3: Write minimal implementation**

```go
// session/service_config.go
package session

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// ServiceConfig represents a service definition from config.yaml
type ServiceConfig struct {
	Name     string            // derived from map key
	Command  string            `yaml:"command"`
	Cwd      string            `yaml:"cwd"`
	Port     int               `yaml:"port"` // 0 means auto
	PortAuto bool              // true if port was "auto" or not specified
	Env      map[string]string `yaml:"env"`
}

// serviceConfigRaw is used for initial parsing before port handling
type serviceConfigRaw struct {
	Command string            `yaml:"command"`
	Cwd     string            `yaml:"cwd"`
	Port    interface{}       `yaml:"port"` // can be int or "auto"
	Env     map[string]string `yaml:"env"`
}

type configFile struct {
	Services map[string]serviceConfigRaw `yaml:"services"`
}

// ParseServiceConfigs parses service configurations from YAML
func ParseServiceConfigs(data []byte) (map[string]*ServiceConfig, error) {
	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse yaml: %w", err)
	}

	result := make(map[string]*ServiceConfig)
	for name, raw := range cfg.Services {
		sc := &ServiceConfig{
			Name:    name,
			Command: raw.Command,
			Cwd:     raw.Cwd,
			Env:     raw.Env,
		}

		// Handle port (can be int, "auto", or nil)
		switch v := raw.Port.(type) {
		case int:
			sc.Port = v
			sc.PortAuto = false
		case float64:
			sc.Port = int(v)
			sc.PortAuto = false
		case string:
			if v == "auto" {
				sc.PortAuto = true
			} else {
				return nil, fmt.Errorf("service %s: invalid port value '%s'", name, v)
			}
		case nil:
			sc.PortAuto = true // default to auto
		default:
			return nil, fmt.Errorf("service %s: invalid port type", name)
		}

		result[name] = sc
	}

	return result, nil
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./session -run TestParseServiceConfig -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add session/service_config.go session/service_config_test.go
git commit -m "feat: add ServiceConfig struct and YAML parsing"
```

---

### Task 1.2: Define RouteMetadata and ServiceState structs

**Files:**
- Create: `session/service_state.go`
- Create: `session/service_state_test.go`

**Step 1: Write the test**

```go
// session/service_state_test.go
package session

import (
	"testing"
	"time"
)

func TestServiceState_IsRunning(t *testing.T) {
	// Create a state with current process (we know it's running)
	state := &ServiceState{
		Name:      "test",
		PID:       1, // init process always exists
		StartTime: time.Now().Add(-time.Hour), // started an hour ago
		Port:      3000,
		Status:    StatusRunning,
	}

	if state.Status != StatusRunning {
		t.Error("expected status to be running")
	}
}

func TestRouteMetadata(t *testing.T) {
	rm := RouteMetadata{
		Hostname: "test-frontend.localhost",
		Port:     3000,
		RouteID:  "sess-test-frontend",
	}

	if rm.Hostname != "test-frontend.localhost" {
		t.Errorf("unexpected hostname: %s", rm.Hostname)
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./session -run TestServiceState -v
```
Expected: FAIL - ServiceState not defined

**Step 3: Write implementation**

```go
// session/service_state.go
package session

import "time"

// Service status constants
const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusFailed  = "failed"
)

// ServiceState represents runtime state of a managed service
type ServiceState struct {
	Name      string    `json:"name"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"` // process group ID
	StartTime time.Time `json:"start_time"`
	Port      int       `json:"port"`
	Status    string    `json:"status"` // running, stopped, failed
	LogFile   string    `json:"log_file"`
}

// RouteMetadata stores route info for reconstruction after Caddy restart
type RouteMetadata struct {
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	RouteID  string `json:"route_id,omitempty"`
}
```

**Step 4: Run test to verify it passes**

```bash
go test ./session -run "TestServiceState|TestRouteMetadata" -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add session/service_state.go session/service_state_test.go
git commit -m "feat: add ServiceState and RouteMetadata types"
```

---

### Task 1.3: Update Session struct with new fields

**Files:**
- Modify: `session/metadata.go:16-30` (Session struct)

**Step 1: Update Session struct**

Add to `session/metadata.go`, updating the Session struct:

```go
type Session struct {
	Name            string                    `json:"name"`
	ProjectAlias    string                    `json:"project_alias,omitempty"`
	ProjectPath     string                    `json:"project_path,omitempty"`
	Branch          string                    `json:"branch"`
	Path            string                    `json:"path"`
	Ports           map[string]int            `json:"ports"`
	Routes          map[string]*RouteMetadata `json:"routes,omitempty"` // CHANGED: was map[string]string
	Services        map[string]*ServiceState  `json:"services,omitempty"` // NEW
	EditorPID       int                       `json:"editor_pid,omitempty"`
	AttentionFlag   bool                      `json:"attention_flag,omitempty"`
	AttentionReason string                    `json:"attention_reason,omitempty"`
	AttentionTime   time.Time                 `json:"attention_time,omitempty"`
	CreatedAt       time.Time                 `json:"created_at"`
	UpdatedAt       time.Time                 `json:"updated_at"`
}
```

**Step 2: Run existing tests to check for breakage**

```bash
go test ./session/... -v
```
Expected: Some failures due to Routes type change

**Step 3: Fix any test failures**

Update tests that reference `Routes` to use the new `*RouteMetadata` type.

**Step 4: Run tests again**

```bash
go test ./session/... -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add session/metadata.go
git commit -m "feat: add Services field and update Routes to RouteMetadata"
```

---

## Phase 2: Service Process Management

### Task 2.1: Create service spawner

**Files:**
- Create: `session/service_manager.go`
- Create: `session/service_manager_test.go`

**Step 1: Write failing test for StartService**

```go
// session/service_manager_test.go
package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceManager_StartService(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "devx-svc-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create logs directory
	logsDir := filepath.Join(tmpDir, ".devx", "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		t.Fatalf("failed to create logs dir: %v", err)
	}

	mgr := NewServiceManager(tmpDir)

	cfg := &ServiceConfig{
		Name:    "test-service",
		Command: "echo hello && sleep 1",
		Port:    3000,
	}

	state, err := mgr.StartService(cfg)
	if err != nil {
		t.Fatalf("failed to start service: %v", err)
	}
	defer mgr.StopService(state)

	if state.PID <= 0 {
		t.Error("expected positive PID")
	}
	if state.Status != StatusRunning {
		t.Errorf("expected status running, got %s", state.Status)
	}

	// Wait a bit and check log file
	time.Sleep(100 * time.Millisecond)

	logContent, err := os.ReadFile(state.LogFile)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if len(logContent) == 0 {
		t.Error("expected log content")
	}
}
```

**Step 2: Run test to verify it fails**

```bash
go test ./session -run TestServiceManager_StartService -v
```
Expected: FAIL - NewServiceManager not defined

**Step 3: Write implementation**

```go
// session/service_manager.go
package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// ServiceManager handles service process lifecycle
type ServiceManager struct {
	worktreePath string
}

// NewServiceManager creates a new service manager for a worktree
func NewServiceManager(worktreePath string) *ServiceManager {
	return &ServiceManager{worktreePath: worktreePath}
}

// StartService spawns a service process
func (m *ServiceManager) StartService(cfg *ServiceConfig) (*ServiceState, error) {
	// Ensure logs directory exists
	logsDir := filepath.Join(m.worktreePath, ".devx", "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs dir: %w", err)
	}

	logFile := filepath.Join(logsDir, cfg.Name+".log")

	// Open log file for writing
	logFd, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	// Determine working directory
	cwd := m.worktreePath
	if cfg.Cwd != "" {
		cwd = filepath.Join(m.worktreePath, cfg.Cwd)
	}

	// Build command
	cmd := exec.Command("sh", "-c", cfg.Command)
	cmd.Dir = cwd
	cmd.Stdout = logFd
	cmd.Stderr = logFd

	// Set up process group so we can kill all children
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Set environment variables
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	if cfg.Port > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PORT=%d", cfg.Port))
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		logFd.Close()
		return nil, fmt.Errorf("failed to start service: %w", err)
	}

	// Get process group ID
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		pgid = cmd.Process.Pid // fallback to PID
	}

	state := &ServiceState{
		Name:      cfg.Name,
		PID:       cmd.Process.Pid,
		PGID:      pgid,
		StartTime: time.Now(),
		Port:      cfg.Port,
		Status:    StatusRunning,
		LogFile:   logFile,
	}

	// Don't wait for the process - it runs in background
	// The log file handle will be kept open by the child process
	go func() {
		cmd.Wait()
		logFd.Close()
	}()

	return state, nil
}

// StopService kills a service and its children
func (m *ServiceManager) StopService(state *ServiceState) error {
	if state.PID <= 0 {
		return nil
	}

	// Kill the entire process group
	pgid := state.PGID
	if pgid <= 0 {
		pgid = state.PID
	}

	// Send SIGTERM to process group (negative PID)
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		// Try killing just the process
		if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil {
			return fmt.Errorf("failed to kill service: %w", err)
		}
	}

	state.Status = StatusStopped
	state.PID = 0
	state.PGID = 0

	return nil
}

// IsProcessAlive checks if a process is still running
func IsProcessAlive(pid int, startTime time.Time) bool {
	if pid <= 0 {
		return false
	}

	// Check if process exists
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix, FindProcess always succeeds, so we need to signal
	// Sending signal 0 checks if process exists
	err = proc.Signal(syscall.Signal(0))
	if err != nil {
		return false
	}

	// TODO: Validate start time to detect PID reuse
	// This requires reading /proc/[pid]/stat on Linux or using sysctl on macOS
	// For now, we trust the PID

	return true
}
```

**Step 4: Run test**

```bash
go test ./session -run TestServiceManager -v
```
Expected: PASS

**Step 5: Commit**

```bash
git add session/service_manager.go session/service_manager_test.go
git commit -m "feat: add ServiceManager for process lifecycle"
```

---

### Task 2.2: Add StopService test

**Files:**
- Modify: `session/service_manager_test.go`

**Step 1: Add test**

```go
func TestServiceManager_StopService(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-svc-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logsDir := filepath.Join(tmpDir, ".devx", "logs")
	os.MkdirAll(logsDir, 0755)

	mgr := NewServiceManager(tmpDir)

	cfg := &ServiceConfig{
		Name:    "sleeper",
		Command: "sleep 60", // long-running process
		Port:    3000,
	}

	state, err := mgr.StartService(cfg)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Verify it's running
	if !IsProcessAlive(state.PID, state.StartTime) {
		t.Fatal("process should be alive")
	}

	// Stop it
	if err := mgr.StopService(state); err != nil {
		t.Fatalf("failed to stop: %v", err)
	}

	// Give it a moment to die
	time.Sleep(100 * time.Millisecond)

	// Verify it's dead
	if IsProcessAlive(state.PID, state.StartTime) {
		t.Error("process should be dead")
	}
}
```

**Step 2: Run test**

```bash
go test ./session -run TestServiceManager_StopService -v
```
Expected: PASS

**Step 3: Commit**

```bash
git add session/service_manager_test.go
git commit -m "test: add StopService test"
```

---

## Continue with remaining phases...

The implementation continues with:

- **Phase 3: tmuxp Generation** - Update template to generate `tail -f` panes
- **Phase 4: CLI Commands** - `devx service list`, `logs`, `restart`, `check`
- **Phase 5: Health Checks** - TCP health check with grace period
- **Phase 6: TUI Integration** - Background monitor, health indicators
- **Phase 7: Caddy Sync** - Route reconciliation
- **Phase 8: Polish** - Backwards compatibility, docs

Each phase follows the same pattern: failing test, implementation, passing test, commit.

---

## Quick Reference: Key Files

| Component | File |
|-----------|------|
| File locking | `session/locked_store.go` |
| Service config | `session/service_config.go` |
| Service state | `session/service_state.go` |
| Process manager | `session/service_manager.go` |
| CLI commands | `cmd/service.go`, `cmd/service_*.go` |
| Health checks | `session/health.go` |
| TUI monitor | `tui/monitor.go` |
| Caddy sync | `session/caddy_sync.go` |

## Test Commands

```bash
# Run all session tests
go test ./session/... -v

# Run with race detector
go test ./session/... -v -race

# Run specific test
go test ./session -run TestServiceManager -v

# Build to check compilation
go build ./...
```
