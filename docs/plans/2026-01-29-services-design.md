# Services Abstraction Design

## Overview

Introduce a formal service abstraction to devx that provides lifecycle management, health monitoring, consistent logging, and self-healing Caddy route synchronization.

## Problem Statement

Currently, services in devx are loosely defined as port allocations with names. This causes several issues:

1. **No lifecycle management** - Services die without notification, no way to restart them
2. **Inconsistent logging** - Logs go to different places, hard for agents to find
3. **Caddy drift** - Routes get out of sync after Caddy restarts or laptop reboots
4. **No health visibility** - No way to know if services are actually healthy

## Design Decisions

This design was reviewed by multiple AI models (Claude agents, GPT-5.2, Gemini Pro) and refined based on two rounds of feedback. Key decisions:

- **Process ownership**: devx owns and manages service processes directly. tmux panes just display log output via `tail -f`. This cleanly separates process management from terminal display.
- **Service lifecycle**: Services start on `session create`, stop on `session rm`. They persist across TUI open/close.
- **Monitoring**: Background health checks while TUI is active. This is proactive service management - if users don't want it, they can use the legacy `ports:` format.
- **Health checks**: TCP-only for v1. Covers 90% of cases without complexity.
- **Restart policy**: Simplified - restart once on failure, then set attention flag.
- **Logging**: Direct file output. tmux panes tail the log files.
- **Concurrency**: File locking for sessions.json (implemented early, Phase 1).

## Design

### Process Ownership Model

**devx owns service processes:**
- `devx session create` spawns service processes as managed background processes
- Processes write stdout/stderr directly to `.devx/logs/<service>.log`
- devx tracks PID + start time in session metadata
- `devx session rm` kills all service processes (using process groups)

**tmux panes display logs:**
- Service panes run `tail -f .devx/logs/<service>.log`
- If pane is killed, service keeps running
- Pane can be recreated to view logs again

**Benefits:**
- Clear process ownership (devx, not tmux)
- Services survive TUI/pane close
- Restart is simple: kill process group, spawn new process
- No pane/process coupling issues

### Service Definition Schema

Services are defined in `.devx/config.yaml`:

```yaml
services:
  frontend:
    cwd: frontend                   # relative to worktree root (optional)
    command: "npm run dev"
    port: auto                      # or specific port like 3000
    env:                            # additional env vars (optional)
      VITE_API_URL: "{{BACKEND_HOST}}"

  backend:
    cwd: backend
    command: "air"
    port: auto
    env:
      DATABASE_URL: "postgres://localhost:5432/dev"
```

**Defaults:**
- `port`: auto (dynamically allocated at session creation, persisted for restarts)
- `cwd`: worktree root
- `log_file`: `.devx/logs/<service>.log`

**Environment variable resolution:** Template variables like `{{BACKEND_HOST}}` are resolved at service start time from current session metadata.

**Backwards Compatibility:**
The old `ports: [ui, api]` format continues to work for port allocation and environment variables. However, managed services require a `command`, so legacy configs won't support the new service management features until migrated.

### Runtime State

Each service tracks minimal runtime state in session metadata:

```go
type ServiceState struct {
    Name      string    // "frontend", "backend"
    PID       int       // process ID (of process group leader)
    PGID      int       // process group ID (for killing children)
    StartTime time.Time // when process started (for PID validation)
    Port      int       // allocated port (persisted across restarts)
    Status    string    // "running", "stopped", "failed"
    LogFile   string    // absolute path to log file
}
```

**Process Groups:** Services are started in their own process group (`setpgid`). This ensures that when we kill a service, we kill all child processes (npm spawns node, air spawns go, etc.).

**PID Validation:** Store start time alongside PID to detect PID reuse. Before signaling a process, verify both PID exists and start time matches.

### Route Storage

Store intended routes with full reconstruction data, not just route IDs:

```go
type RouteMetadata struct {
    Hostname string // "jf-update-svc-frontend.localhost"
    Port     int    // 3421
    RouteID  string // "sess-jf-update-svc-frontend" (for cleanup)
}

// In Session struct:
Routes map[string]RouteMetadata `json:"routes,omitempty"` // service -> route metadata
```

**Rationale:** Route IDs change when Caddy restarts. Storing hostname + port allows deterministic route reconstruction.

### Health Checks

**v1: TCP only**

Attempt TCP connection to `127.0.0.1:PORT` (explicit IPv4 to avoid IPv4/IPv6 mismatch). If connection succeeds, service is healthy.

```go
const (
    HealthCheckInterval = 10 * time.Second
    HealthCheckTimeout  = 5 * time.Second
    HealthCheckGrace    = 30 * time.Second
)
```

**Startup Grace Period:** New services get 30 seconds before health checks begin. This prevents killing slow-starting services (npm install, cargo build, etc.).

**Future (v2+):** HTTP health checks with configurable path, command-based health checks.

### Restart Policy

Simplified restart behavior:

1. Service fails health check (or process exits unexpectedly)
2. Attempt one restart (kill process group, start again)
3. If restart fails health check, set status to "failed" and set session attention flag
4. No automatic retry - user must manually intervene via `devx service restart`

This avoids complex state tracking while still providing value. Users can always manually restart.

### Log Management

Logs are stored in `.devx/logs/<service>.log` within each session's worktree.

**Implementation:**
- Service process stdout/stderr redirected directly to log file
- tmux pane runs `tail -f .devx/logs/<service>.log` for live view
- Logs accumulate for session lifetime
- Removed when session is deleted
- Log directory created on session create

**Agent Access:**
Agents working in a session can read logs at predictable paths:
- `.devx/logs/frontend.log`
- `.devx/logs/backend.log`

### Concurrency & File Locking

**Problem:** Multiple devx processes (TUI + CLI commands) can write to sessions.json simultaneously.

**Solution:** Implement locking wrapper as Phase 1 infrastructure:

```go
type LockedSessionStore struct {
    path string
    lock *flock.Flock  // separate .lock file
}

func (ls *LockedSessionStore) Update(fn func(*SessionStore) error) error {
    if err := ls.lock.Lock(); err != nil {
        return err
    }
    defer ls.lock.Unlock()

    store, err := LoadSessions()
    if err != nil {
        return err
    }
    if err := fn(store); err != nil {
        return err
    }
    return store.Save()  // atomic: write temp file, rename
}
```

### Monitoring

A background monitor runs as a goroutine when TUI is active:

1. **Health check loop** - Every 10s, check each service's TCP port
2. **Process check** - Verify tracked PIDs still exist (handle unexpected exits)
3. **Restart handler** - Attempt one restart on failure, then set attention flag
4. **Caddy sync** - Every 30s, verify routes match session state

**On-demand:** `devx service check` for when TUI isn't running.

**Scope:** Services run independently of TUI. Monitoring is active when TUI is open. If TUI isn't running, services keep running but aren't monitored.

### Caddy Sync & Self-Healing

**Sync Logic:**
- Compare session metadata (intended routes) against actual Caddy routes
- Missing in Caddy -> recreate from metadata
- Orphan in Caddy -> remove **only if route ID matches devx naming pattern** (`sess-*`)

**Orphan Safety:** Only delete routes that match devx naming convention to avoid deleting user-created routes on shared Caddy instances.

**Sync Triggers:**
- TUI startup (initial sync)
- Every 30s while TUI running
- `devx session attach` (before attaching)
- `devx session create` (after creation)
- `devx service sync` (on-demand)

**Scenarios Handled:**
- Caddy restart -> routes gone -> recreate from hostname+port in metadata
- Session removed uncleanly -> orphan routes -> cleanup (only devx routes)
- Laptop reboot -> full reconciliation on next devx use

### CLI Interface

```bash
# List services and status
devx service list [session]
devx service ls [session]

# View logs
devx service logs <service> [session]
devx service logs frontend --follow
devx service logs backend --lines 100

# Lifecycle
devx service restart <service> [session]

# Health and sync
devx service check [session]    # check health, attempt restarts
devx service sync [session]     # repair Caddy routes
```

Session defaults to current (detected from cwd or tmux session name).

### TUI Integration

**Session list view** - Health indicator per session:
```
  SESSION          BRANCH              SERVICES    STATUS
> jf-update-svc    jf-update-services  ***         3/3 healthy
  main-dev         main                **          2/2 healthy
  bugfix-auth      fix/auth-flow       **          1/2 healthy !
```

**"h" key - Services & Routes view:**
```
Services & Routes: jf-update-svc

  SERVICE   STATUS   HEALTH    PORT   ROUTE
> * frontend running  healthy   3421   https://jf-update-svc-frontend.localhost
  * backend  running  unhealthy 8080   https://jf-update-svc-backend.localhost
  o worker   stopped  -         -      -

[Enter] open route  [r] restart  [l] logs  [c] copy  [q] back
```

### tmuxp Template Generation

**Config structure:**
```yaml
services:
  frontend:
    command: "npm run dev"
    cwd: frontend
  backend:
    command: "air"
    cwd: backend

windows:  # User-defined windows
  - name: editor
    panes:
      - claude
  - name: infra
    cwd: infra
    panes:
      - "tofu init && tofu workspace select dev"
      - ""
```

**Generated tmux structure:**
1. User-defined windows (editor, infra, etc.)
2. Auto-generated "services" window with one pane per service

**Service pane command:**
```bash
tail -f .devx/logs/frontend.log
```

The actual service process is spawned separately by devx on session create, not by the tmux pane.

**Custom templates:**
`tmuxp_template:` config still supported for full customization.

## Implementation Plan

### Phase 0: File Locking Infrastructure
- Implement `LockedSessionStore` wrapper
- Atomic writes (temp file + rename)
- Separate `.lock` file for locking
- All subsequent phases use this for metadata access

### Phase 1: Schema & Config Parsing
- Define service config schema in Go structs
- Parse `services:` block from config.yaml
- Update route storage schema to `RouteMetadata`
- Derive service list and allocate ports
- Store ports in session metadata

### Phase 2: Service Process Management
- Implement service spawning with process groups
- Redirect stdout/stderr to log files
- Track PID + PGID + start time in session metadata
- Implement service stop (kill process group)
- Create `.devx/logs/` directory on session create

### Phase 3: tmuxp Generation
- Update tmuxp template for services window
- Service panes run `tail -f` on log files
- User-defined windows unchanged

### Phase 4: CLI Commands
- `devx service list` - show services and status (validate PIDs)
- `devx service logs` - tail/view log files
- `devx service restart` - kill process group and respawn
- `devx service check` - manual health check

### Phase 5: Health Checks & Restart
- Implement TCP health check (127.0.0.1 explicit)
- Add startup grace period
- Detect unexpected process exits
- Implement one-retry restart logic
- Integrate with attention flags

### Phase 6: TUI Integration
- Update session list with health indicators
- Extend "h" view to services & routes
- Add keyboard shortcuts for service actions
- Implement background monitor goroutine

### Phase 7: Caddy Sync
- Implement route reconciliation logic
- Add sync triggers (startup, periodic, on-demand)
- Handle orphan cleanup (devx routes only)

### Phase 8: Migration & Polish
- Backwards compatibility testing for `ports:` format
- Migration guide for existing configs
- Documentation

## Future Enhancements (v2+)

These were considered but deferred to keep v1 focused:

- **HTTP health checks** - Check specific endpoints with expected status codes
- **Command health checks** - Run custom health check scripts
- **Complex restart policies** - Exponential backoff, max attempts with windows
- **Service dependencies** - Start order, wait for healthy before starting dependents
- **Log rotation** - Size limits, automatic rotation

## Review Feedback Addressed

This design was refined based on feedback from 5 reviewers across 2 rounds:

| Concern | Resolution |
|---------|------------|
| Process ownership unclear | devx owns processes, tmux panes just tail logs |
| TUI goroutine can't restart pane processes | No longer an issue - devx owns processes directly |
| FIFO log capture is complex | Direct file output, panes tail the files |
| PID reuse can kill wrong process | Track PID + start time |
| npm/air spawn children | Use process groups (PGID), kill the group |
| Race conditions on sessions.json | File locking as Phase 0 |
| Route IDs change on Caddy restart | Store hostname + port, not just route ID |
| Caddy orphan cleanup unsafe | Only delete routes matching devx pattern |
| localhost IPv4/IPv6 mismatch | Use explicit 127.0.0.1 |
| Complex restart windows | Simplified to one-retry |
| HTTP/command health checks | Deferred to v2 |
| Missing startup grace period | Added 30s default |
