# DevX Docker + Gatepost Integration Plan

**Status:** Draft v3 (post-grilling)
**Date:** 2025-07-14

---

## Overview

This plan adds optional Docker-based session isolation to DevX in two phases:

1. **Phase 1 — Docker Target:** DevX natively manages per-session Docker containers. Worktrees, ports, Caddy, and Cloudflare continue working as-is. tmux runs inside the container. No Gatepost dependency. No in-container attention flags.
2. **Phase 2 — Gatepost Target:** Add Gatepost as an external isolation backend. DevX launches sessions via Gatepost's Compose topology, gaining network-enforced egress control, secret injection, audit logging, and webhook-based attention flags. Gatepost has its own configuration; DevX just knows how to call it.

### Design Principles

- **Target as a small abstraction.** A `Target` interface with `Start`/`Stop` lets DevX dispatch session lifecycle to different backends (`host`, `docker`, future `gatepost`, `vm`, `remote`) without touching the rest of the pipeline.
- **Same session experience regardless of target.** tmux runs inside the container with the same tmuxp template. The user doesn't know or care whether they're in a container.
- **Gatepost is a separate project with its own config.** DevX doesn't embed Gatepost configuration. Phase 2 means DevX knows how to launch a Gatepost session — not that DevX manages Gatepost internals.
- **Caddy/Cloudflare are untouched.** Both reverse-proxy to `localhost:<port>`. Docker targets just publish container ports to those same host loopback ports.

---

## 1. Current Code Paths Involved

### Session Creation (`cmd/session_create.go` → `runSessionCreate`)

Pipeline:
1. Validate name, resolve project (from `--project` flag or CWD)
2. Load project/global config for port service names (`config.Config.Ports`)
3. Optional `git pull` if `AutoPullOnCreate` is set
4. Allocate ports via `session.AllocatePorts()` — uses `go-getport` for random free ports
5. Create git worktree via `session.CreateWorktree()`
6. Copy bootstrap files via `session.CopyBootstrapFiles()`
7. Save session metadata via `store.AddSessionWithProject()` — writes `~/.config/devx/sessions.json`
8. Build hostname maps via `caddy.BuildHostname()` / `caddy.BuildExternalHostname()`
9. Generate `.envrc` via `session.GenerateEnvrc()`
10. Generate `.tmuxp.yaml` via `session.GenerateTmuxpConfig()`
11. Update session routes in metadata
12. `syncAllCaddyRoutes()` — rebuilds full `caddy-config.json`, reloads Caddy
13. `syncAllCloudflareRoutes()` — rebuilds cloudflared YAML, reloads daemon
14. Launch tmux unless `--no-tmux`

### Session Removal (`cmd/session_rm.go` → `runSessionRm`)

Pipeline:
1. Confirm deletion (unless `--force`)
2. Terminate editor process
3. Kill tmux session
4. Archive retained artifacts
5. Run cleanup command (from config `cleanup_command`, via shell)
6. Remove git worktree
7. Delete metadata from `sessions.json`, reconcile slots
8. `syncAllCaddyRoutes()`, `syncAllCloudflareRoutes()`

### Key insight for Docker integration

Caddy and Cloudflare both reverse-proxy to `localhost:<port>` on the host. Docker targets just need to publish container service ports to those same host loopback ports. The entire Caddy/Cloudflare pipeline remains unchanged.

---

## 2. Proposed Config Schema Changes

### Global config (`~/.config/devx/config.yaml`)

```yaml
# Existing fields unchanged...
basedomain: localhost
caddy_api: http://localhost:2019
ports:
  - ui
  - api

# New: target and Docker defaults
target: host                     # default target: host | docker | gatepost
docker:
  image: "devx-session-base:latest"  # default agent image
  memory_limit: "4g"
  cpu_limit: "2"
  pids_limit: 256
  read_only_root: false
```

### Project-level config (`.devx/config.yaml`)

```yaml
# Per-project overrides
target: docker                   # default target for this project
docker:
  image: "my-custom-agent:latest"
  memory_limit: "8g"
```

### Session-level (flag overrides)

```
devx session create my-session                            # uses config default (host)
devx session create my-session --target docker
devx session create my-session --target host
devx session create my-session --target docker --image my-image:latest
```

Gatepost remains the owner of policy and secret-injection semantics, but DevX carries a small trusted host-integration contract so it can launch/link a Gatepost-backed session. Executable Gatepost settings are read only from explicit environment/CLI config or the user-global DevX config, not from project `.devx/config.yaml`:

```yaml
gatepost:
  agent_image: gatepost-pi-agent:latest
  logs_command: gatepost-logs                  # preferred packaged artifact
  provider_bootstrap_command: gatepost-provider-bootstrap
  root: /path/to/gatepost-checkout             # local-development fallback
  auth_home: ~/.pi-auth-home                   # optional alternate Pi auth home
  required_providers: anthropic-oauth,codex-oauth,openai-key   # strict readiness gate
```

DevX-generated per-session `policy.gatepost.yaml` is the MVP bootstrap policy; provider registration is delegated to the configured Gatepost bootstrap command when available, with host-env registration as a fallback.

---

## 3. Proposed Session Metadata Changes

Add to `Session` struct in `session/metadata.go`:

```go
type Session struct {
    // ... existing fields ...

    // Target metadata (zero values for host target)
    Target TargetMeta `json:"target,omitempty"`
}

// TargetMeta is persisted in sessions.json.
type TargetMeta struct {
    Type          string `json:"type"`                     // "host", "docker", future: "gatepost", "vm"
    ContainerID   string `json:"container_id,omitempty"`   // Docker container ID
    ContainerName string `json:"container_name,omitempty"` // Docker container name
    NetworkName   string `json:"network_name,omitempty"`   // Docker network name
    Image         string `json:"image,omitempty"`          // Image used
}
```

Notes:
- Intentionally small. No Gatepost fields — those belong to Gatepost's own session state.
- No `HostPorts` — `Session.Ports` remains the single source of truth for host ports.
- `TargetMeta` stores only what's needed to stop/remove/recover the target.

### Backward compatibility

- `Target.Type` defaults to `""` which is treated as `"host"` everywhere
- All existing sessions continue working without any migration
- `Ports` field remains the canonical port map for Caddy/Cloudflare

```go
func (s *Session) TargetType() string {
    if s.Target.Type == "" {
        return "host"
    }
    return s.Target.Type
}

func (s *Session) IsContainerized() bool {
    return s.TargetType() != "host"
}
```

---

## 4. New Packages/Files/Functions

### Target interface

```go
// target/target.go

// Target is the interface that isolation backends implement.
type Target interface {
    // Type returns the target identifier ("host", "docker", etc.)
    Type() string
    // Start creates and starts the execution environment for a session.
    Start(ctx context.Context, opts StartOpts) (*StartResult, error)
    // Stop tears down the execution environment. Must be idempotent.
    Stop(ctx context.Context, meta session.TargetMeta) error
}

type StartOpts struct {
    SessionName  string
    WorktreePath string
    HostPorts    map[string]int    // service -> host port to publish
    Image        string
    Env          map[string]string
    Labels       map[string]string
}

type StartResult struct {
    Meta session.TargetMeta
}
```

Intentionally minimal — `Start` and `Stop` only. `Exec`, `IsRunning`, and other capabilities are concrete methods on `DockerTarget`, not interface methods. More methods are added to the interface only when a second backend needs them.

### Phase 1 file layout

```
target/                          # New package
  target.go                      # Target interface, StartOpts, StartResult, resolve helper
  host.go                        # HostTarget (no-op Start/Stop)
  docker.go                      # DockerTarget implementation
  docker_security.go             # SecurityOpts, DefaultSecurityOpts()
  docker_test.go
  exec.go                        # ExecInSession() helper — host vs container dispatch

docker/                          # Dockerfile for devx-session-base
  Dockerfile

session/
  metadata.go                    # Add TargetMeta to Session struct

cmd/
  session_create.go              # Add --target, --image flags; dispatch to Target
  session_rm.go                  # Dispatch to Target.Stop() before worktree removal
  session_exec.go                # New: `devx session exec <name> <cmd...>`
```

---

## 5. Phase 1: Docker Target

### Goal

`devx session create my-session --target docker` creates a worktree, starts a Docker container with the worktree mounted, launches tmux inside the container, publishes service ports to the host, and everything else (Caddy, Cloudflare, TUI) works unchanged. The session experience is identical to a host session.

### Implementation steps

#### 5.1 Docker availability check

Before any side effects (worktree creation, port allocation), check that Docker is running:

```go
if targetType == "docker" {
    if err := exec.Command("docker", "info").Run(); err != nil {
        return fmt.Errorf("Docker is not running. Start Docker Desktop or install Docker to use --target docker.")
    }
}
```

#### 5.2 Base image (`docker/Dockerfile`)

```dockerfile
FROM ubuntu:24.04

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl wget git build-essential \
    python3 python3-pip \
    nodejs npm \
    tmux \
    && pip3 install --break-system-packages tmuxp \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /workspace
```

Auto-built on first `--target docker` use:

```go
if targetType == "docker" && !imageExistsLocally("devx-session-base:latest") {
    fmt.Println("Building devx-session-base image (first time only)...")
    // docker build -t devx-session-base:latest docker/
}
```

#### 5.3 Target package

```go
// target/host.go

type HostTarget struct{}

func (h *HostTarget) Type() string { return "host" }
func (h *HostTarget) Start(ctx context.Context, opts StartOpts) (*StartResult, error) {
    return &StartResult{Meta: session.TargetMeta{Type: "host"}}, nil
}
func (h *HostTarget) Stop(ctx context.Context, meta session.TargetMeta) error { return nil }
```

```go
// target/docker.go

type DockerTarget struct{}

func (d *DockerTarget) Type() string { return "docker" }

func (d *DockerTarget) Start(ctx context.Context, opts StartOpts) (*StartResult, error) {
    name := containerName(opts.SessionName)    // devx-<sanitized>
    netName := networkName(opts.SessionName)   // devx-<sanitized>-net

    // 1. Create per-session bridge network
    //    docker network create <netName>

    // 2. Create and start container
    //    docker run -d --name <name>
    //      --network <netName>
    //      --restart unless-stopped
    //      -v <worktree>:/workspace
    //      -w /workspace
    //      -p 127.0.0.1:<port>:<port> (for each service port)
    //      --cap-drop=ALL --security-opt no-new-privileges
    //      --pids-limit 256 --memory 4g --cpus 2
    //      <image> sleep infinity

    // 3. Get container ID from docker inspect

    return &StartResult{
        Meta: session.TargetMeta{
            Type:          "docker",
            ContainerID:   containerID,
            ContainerName: name,
            NetworkName:   netName,
            Image:         opts.Image,
        },
    }, nil
}

func (d *DockerTarget) Stop(ctx context.Context, meta session.TargetMeta) error {
    // docker stop <container> (ignore not-found)
    // docker rm <container> (ignore not-found)
    // docker network rm <network> (ignore not-found)
    // Fully idempotent.
    return nil
}

// Exec runs a command inside the container.
func (d *DockerTarget) Exec(ctx context.Context, meta session.TargetMeta, cmd []string, interactive bool) error {
    // docker exec [-it] <container> <cmd...>
}

// IsRunning checks if the container is alive.
func (d *DockerTarget) IsRunning(ctx context.Context, meta session.TargetMeta) (bool, error) {
    // docker inspect --format '{{.State.Running}}' <container>
}
```

```go
// target/exec.go

// ExecInSession runs a command in the session's execution environment.
// For host sessions, runs directly. For Docker sessions, wraps with docker exec.
// Used by CLI attach, session exec, web UI ttyd, and tmuxp launch.
func ExecInSession(meta session.TargetMeta, cmd []string, interactive bool) *exec.Cmd {
    if meta.Type == "" || meta.Type == "host" {
        return exec.Command(cmd[0], cmd[1:]...)
    }
    args := []string{"exec"}
    if interactive {
        args = append(args, "-it")
    }
    args = append(args, meta.ContainerName)
    args = append(args, cmd...)
    return exec.Command("docker", args...)
}
```

Container and network naming uses the existing `SanitizeHostname` logic:

```go
func containerName(sessionName string) string {
    return "devx-" + caddy.SanitizeHostname(sessionName)
}

func networkName(sessionName string) string {
    return "devx-" + caddy.SanitizeHostname(sessionName) + "-net"
}
```

#### 5.4 Security defaults

```go
// target/docker_security.go

type SecurityOpts struct {
    MemoryLimit  string   // "4g"
    CPULimit     string   // "2"
    PidsLimit    int      // 256
    ReadOnlyRoot bool
    CapDrop      []string // ["ALL"]
    NoNewPrivs   bool
    TmpfsMounts  []string // ["/tmp", "/var/tmp"] when read-only
}

func DefaultSecurityOpts() SecurityOpts {
    return SecurityOpts{
        MemoryLimit:  "4g",
        CPULimit:     "2",
        PidsLimit:    256,
        ReadOnlyRoot: false,
        CapDrop:      []string{"ALL"},
        NoNewPrivs:   true,
    }
}
```

#### 5.5 Session creation changes

In `cmd/session_create.go`, the target dispatch wraps the existing pipeline:

```go
// Early: check Docker availability if needed
targetType := resolveTargetType(cfg, targetFlag) // flag > project config > global config > "host"
if targetType == "docker" {
    if err := checkDockerAvailable(); err != nil {
        return err
    }
    if err := ensureBaseImage(); err != nil {
        return err
    }
}

// ... existing pipeline: worktree, ports, bootstrap, envrc, tmuxp ...

// After worktree + ports + envrc + tmuxp are written:
tgt := resolveTarget(targetType) // returns &HostTarget{} or &DockerTarget{}

result, err := tgt.Start(ctx, target.StartOpts{
    SessionName:  name,
    WorktreePath: worktreePath,
    HostPorts:    portAllocation.Ports,
    Image:        resolveImage(cfg, imageFlag), // flag > project config > global config
    Env:          buildContainerEnv(envData),
    Labels: map[string]string{
        "devx.session": name,
        "devx.project": projectAlias,
    },
})
if err != nil {
    return fmt.Errorf("failed to start %s target: %w", targetType, err)
}

store.UpdateSession(name, func(s *session.Session) {
    s.Target = result.Meta
})

// For Docker sessions: exec tmuxp load inside the container
// For host sessions: launch tmux as today
if targetType == "docker" {
    // docker exec <container> tmuxp load /workspace/.tmuxp.yaml -d -s <session>
} else {
    // existing tmux launch
}
```

#### 5.6 Session removal changes

In `cmd/session_rm.go`, before worktree removal:

```go
// Run cleanup command inside the container for Docker sessions
if sess.IsContainerized() {
    if cleanupCmd := viper.GetString("cleanup_command"); cleanupCmd != "" {
        target.ExecInSession(sess.Target, []string{"sh", "-c", cleanupCmd}, false)
    }
} else {
    // existing host cleanup
    session.RunCleanupCommandForShell(sess)
}

// Stop the target (container + network for Docker; no-op for host)
tgt := resolveTarget(sess.TargetType())
if err := tgt.Stop(ctx, sess.Target); err != nil {
    fmt.Printf("Warning: failed to stop %s target: %v\n", sess.TargetType(), err)
}
```

#### 5.7 Session attach changes

`devx session attach` becomes target-aware:

```go
if sess.IsContainerized() {
    // Ensure container is running (restart: unless-stopped handles most cases)
    if !dockerTarget.IsRunning(ctx, sess.Target) {
        return fmt.Errorf("Container for session '%s' is not running. Remove and recreate the session.", name)
    }
    // Attach to tmux inside the container
    cmd := target.ExecInSession(sess.Target, []string{"tmux", "attach", "-t", name}, true)
    cmd.Stdin = os.Stdin
    cmd.Stdout = os.Stdout
    cmd.Stderr = os.Stderr
    return cmd.Run()
} else {
    // existing host attach
    session.AttachTmuxSession(name)
}
```

#### 5.8 Session exec command

New `cmd/session_exec.go`:

```
devx session exec my-session -- npm test
devx session exec my-session --shell    # interactive shell
```

For Docker sessions, uses `target.ExecInSession()`. For host sessions, runs the command directly in the worktree.

#### 5.9 Security posture

Default container security:
- `--cap-drop=ALL`
- `--security-opt no-new-privileges`
- `--pids-limit 256`
- `--memory 4g`
- `--cpus 2`
- No Docker socket mount
- No host network mode
- No privileged mode
- Only the worktree mounted (at `/workspace`)
- Per-session bridge network

#### 5.10 Container restart recovery

Containers are created with `restart: unless-stopped`. If Docker Desktop restarts, containers come back up automatically with the worktree still mounted.

For `attach`, if the container isn't running, print a clear error message. Full `EnsureRunning` (recreating deleted containers from metadata) is a follow-up — the restart policy handles the common case.

#### 5.11 Attention flags

Phase 1 Docker sessions **do not support** attention flags from inside the container. Host-side `devx session flag` still works.

Phase 2 with Gatepost provides in-container attention flags via Gatepost's generic webhook system.

#### 5.12 Web UI / ttyd

The web UI launches ttyd pointed at a tmux session. For Docker sessions, the ttyd command wraps with `docker exec`:

```go
// Uses the same ExecInSession helper
cmd := target.ExecInSession(sess.Target, []string{"tmux", "attach", "-t", sessionName}, true)
```

---

## 6. Phase 2: Gatepost Integration (sketch)

Phase 2 adds Gatepost as an external isolation backend. Gatepost's internal architecture is documented in the Gatepost repo. This section covers only the DevX side.

### Goal

`devx session create my-session --target gatepost` creates a Gatepost-managed session with network-enforced egress, audit logging, and webhook-based attention flags.

### How it works

1. DevX creates the worktree and allocates host ports (same as today)
2. DevX calls Gatepost to start a session (via Compose or future Gatepost CLI)
3. Gatepost starts the proxy + agent containers with enforced networking
4. DevX passes host ports so the agent container publishes them
5. Caddy/Cloudflare route to those host ports (unchanged)
6. On removal, DevX calls Gatepost to stop the session, then removes the worktree

### Policy file isolation

Policy files must NOT be in the workspace. The agent must not be able to read what it can and can't access.

Discovery order (all on the host, never inside the container):
1. `--policy <host-path>` flag
2. `.devx/gatepost.yaml` in the project root (not the worktree)
3. No policy = audit mode

DevX validates that the resolved policy path is not under the worktree path before mounting.

### DevX-side changes

- `target/gatepost.go` — implements `Target` interface, wraps Gatepost Compose
- `cmd/gatepost.go` — thin `devx gatepost audit <session>` for viewing audit logs
- Webhook registration at session creation for attention flags

### What stays in Gatepost

Everything else: policy engine, proxy, secret injection, control plane, audit logging, webhook dispatch. DevX talks to Gatepost over HTTP, doesn't import Gatepost Go packages.

---

## 7. Migration / Backward Compatibility

### Zero migration required

- Existing sessions have no `Target` field → treated as `"host"`
- All existing config files work unchanged
- `devx session create` without `--target` defaults to `"host"`
- Caddy/Cloudflare sync code doesn't change
- tmux behavior unchanged for host sessions
- `sessions.json` gains optional new fields; old DevX binaries ignore unknown JSON

---

## 8. Test Plan

### Unit tests
- `TestDefaultSecurityOpts` — verify sane defaults
- `TestTargetMetaSerialization` — JSON round-trip
- `TestBackwardCompatHost` — empty Target treated as host
- `TestContainerName` — sanitization of session names with slashes/dots
- `TestExecInSession` — correct command construction for host vs docker

### Integration tests (require Docker)
- `TestCreateAndRemoveContainer` — full lifecycle
- `TestPortPublishing` — service ports reachable from host
- `TestWorktreeMount` — `/workspace` contains worktree files
- `TestSecurityDefaults` — verify cap_drop, no-new-privileges, limits
- `TestNoDockerSocket` — Docker socket not accessible in container
- `TestPerSessionNetwork` — containers on different sessions can't communicate
- `TestTmuxInsideContainer` — tmuxp loads and tmux session is accessible
- `TestSessionCreateDocker` — end-to-end `devx session create --target docker`
- `TestSessionRmDocker` — container + network cleaned up
- `TestCleanupCommandInContainer` — cleanup_command runs inside container
- `TestCaddyStillWorks` — Caddy routes resolve to Docker-published ports
- `TestExistingHostSessions` — host sessions unaffected
- `TestAutoBuildBaseImage` — image built on first use if missing

### Manual verification
- [ ] `devx session create test` works exactly as before (no --target flag)
- [ ] `devx session create test --target docker` starts container, tmux inside, ports work
- [ ] `devx session attach test` connects to tmux inside container
- [ ] `devx session exec test -- ls /workspace` shows worktree files
- [ ] `devx session rm test` cleans up container and network
- [ ] TUI shows target type indicator
- [ ] Web UI ttyd connects to in-container tmux

---

## 9. Risks and Open Questions

### Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| Docker Desktop not running | Session create fails | `docker info` check before any side effects, clear error message |
| Port conflicts | Service unreachable | `go-getport` allocates free host ports; same port used in container |
| Container startup latency | Slower session creation | Base image cached locally, auto-built once |
| File permission mismatches | Container can't write to worktree | Docker Desktop handles this on macOS; Linux needs `--user` |
| tmux not in custom image | Session create fails after container starts | Detect missing tmux/tmuxp, clear error |
| Base image stale | Missing tools after DevX update | Document rebuild; consider version tag |

### Open

1. **Linux UID mapping:** Docker Desktop handles this on macOS. Linux needs `--user $(id -u):$(id -g)` or userns-remap.
2. **`EnsureRunning` scope:** `restart: unless-stopped` handles Docker restart. Full container recreation from metadata is a follow-up.
3. **Web UI ttyd:** Small change to command construction — verify ttyd can wrap `docker exec`.

---

## Implementation Order

### Phase 1

1. Add `TargetMeta` to session metadata + backward compat helpers
2. Add `target` + `docker` config sections
3. Create `target/` package: `Target` interface, `HostTarget`, `DockerTarget`, `ExecInSession`
4. Create `docker/Dockerfile` for `devx-session-base`, auto-build logic
5. Add `--target` and `--image` flags to `session create`
6. Add Docker availability check (early fail)
7. Wire target dispatch into create pipeline (start container, exec tmuxp inside)
8. Wire `tgt.Stop()` into remove pipeline (cleanup inside container, then stop)
9. Update attach to be target-aware (`ExecInSession` for tmux attach)
10. Add `devx session exec` command
11. Test with Caddy and Cloudflare
12. Update TUI to show target type indicator
13. Update web UI ttyd to use `ExecInSession`

### Phase 2 (future, separate branch)

1. Implement `GatepostTarget` in `target/gatepost.go`
2. Policy file discovery + workspace-path validation
3. Compose override generation
4. Wire Gatepost start/stop into create/remove
5. Webhook registration for attention flags
6. `devx gatepost audit` command
