# Gatepost Changes Required for DevX Integration

**Status:** Draft v2 (post-review)  
**Date:** 2025-07-14  
**Context:** DevX wants to use Gatepost as the isolation + egress control layer for Docker-based agent sessions. This document lists changes needed in the Gatepost repo. Gatepost remains a separate project with its own configuration — these are improvements to Gatepost that make it a better building block for any orchestrator (DevX, Hermes, standalone CLI).

---

## 1. Policy File Must Not Be in the Workspace

### Problem

Currently `compose/session.yml` mounts `${GATEPOST_WORKSPACE}:/workspace` into **both** the proxy and agent containers. Scripts like `pi-session-up` set `GATEPOST_POLICY_FILE=/workspace/examples/pi-policy.gatepost.yaml` — meaning the agent can read the policy and learn exactly what it can/can't access.

An agent that knows the allow/deny rules can game them (e.g., exfiltrate data through an allowed host, avoid triggering deny rules by encoding traffic differently, or social-engineer broader access by referencing the allowed list).

### Changes

**`compose/session.yml`:**

- Remove the workspace mount from the proxy container entirely (it doesn't need it).
- Add an optional policy volume mount for the proxy only:

```yaml
services:
  proxy:
    volumes:
      - ${GATEPOST_AUDIT_DIR}:/audit
      # Policy file mounted from host — NOT the workspace.
      # Empty/unset = audit mode (no policy).
      - ${GATEPOST_POLICY_FILE_HOST:-/dev/null}:/etc/gatepost/policy.yaml:ro
    environment:
      GATEPOST_POLICY_FILE: ${GATEPOST_POLICY_MOUNT:-}
      # When GATEPOST_POLICY_FILE_HOST is set, GATEPOST_POLICY_MOUNT should be /etc/gatepost/policy.yaml
      # When empty, GATEPOST_POLICY_FILE stays empty = audit mode

  agent:
    volumes:
      - ${GATEPOST_WORKSPACE}:/workspace
      # NO policy file here
```

The key: the policy file path on the host (`GATEPOST_POLICY_FILE_HOST`) is separate from the workspace path. The proxy reads it from `/etc/gatepost/policy.yaml` inside the container. The agent never sees it.

**Alternative (simpler env approach):** Keep a single `GATEPOST_POLICY_FILE` env var but document/enforce that it must NOT be under `/workspace`. DevX's Compose override would mount the policy to a proxy-only path like `/etc/gatepost/policy.yaml`.

**`scripts/pi-session-up`:**

- Stop setting `GATEPOST_POLICY_FILE` to a workspace-relative path.
- Instead, mount the policy file from the host project root (not the worktree):

```bash
# Before (agent can read):
export GATEPOST_POLICY_FILE="/workspace/examples/pi-policy.gatepost.yaml"

# After (agent cannot read):
export GATEPOST_POLICY_FILE_HOST="$REPO_ROOT/examples/pi-policy.gatepost.yaml"
export GATEPOST_POLICY_MOUNT="/etc/gatepost/policy.yaml"
```

**`scripts/gatepost-up`:**

- Accept `--policy <host-path>` flag.
- Mount the policy file into the proxy container only.
- Default: no policy (audit mode).

---

## 2. Agent-Facing Listener + Generic Webhooks

### Problem

Gatepost is designed to be consumed by multiple orchestrators (DevX, Hermes/OpenClaw, any CLI). Those orchestrators need to know about events happening inside the session — the agent signaling it needs attention, a policy decision being escalated, a phase transition completing, etc.

This should be a **generic, configurable webhook system** in Gatepost, not a DevX-specific feature. Gatepost fires events; consumers register webhook URLs to receive them.

### Design

Two pieces:

1. **Agent-facing listener** — a third HTTP listener on the proxy container, reachable from the agent on the internal network. Exposes a small, safe, write-only API. The agent can post events ("I'm done", "I need help") without being able to read policy, rules, secrets, or session state.

2. **Webhook dispatch** — when the agent posts an event (or when the policy engine itself generates an event like an escalation or a deny), Gatepost fires a webhook to one or more configured URLs. Webhooks are configured at session creation via env vars or the control plane API.

### Listener architecture

Three listeners in the proxy container, each with a different trust level:

| Listener | Bind address | Reachable from | Purpose |
|----------|-------------|----------------|----------|
| Internal | `127.0.0.1:18081` | mitmproxy addon only | Policy evaluation, secret injection |
| Control  | `127.0.0.1:18082` | Host only (via port publish) | Phase/rules/secrets management (orchestrator) |
| Agent    | `0.0.0.0:18083` | Agent container (internal net) | Write-only event submission |

### Agent-facing API

```
GET  /agent/healthz              Health check
POST /agent/event                Submit an event
```

Event payload:

```json
{
  "type": "flag",
  "reason": "Claude Done",
  "metadata": {}              
}
```

Supported event types:
- `flag` — agent wants human attention (maps to DevX attention flags, Hermes escalation, etc.)
- `flag_clear` — agent no longer needs attention
- `status` — agent status update (informational, no action required)
- `custom` — opaque event for orchestrator-specific use

The agent-facing API is deliberately minimal — no reads, no state queries, no policy access.

### Webhook configuration

Webhooks are registered via environment variable (at session creation) or the control plane API (at runtime).

**Env var approach (simple, set at session creation):**

```bash
# JSON array of webhook configs
GATEPOST_WEBHOOKS='[
  {
    "url": "http://host.docker.internal:7777/api/sessions/flag?name=my-session",
    "events": ["flag", "flag_clear"],
    "headers": {"Authorization": "Bearer <token>"},
    "timeout_ms": 3000
  },
  {
    "url": "http://host.docker.internal:9090/hermes/callback",
    "events": ["*"],
    "headers": {},
    "timeout_ms": 5000
  }
]'
```

**Control plane API (runtime registration):**

```
GET    /webhooks            List registered webhooks
POST   /webhooks            Register a webhook
DELETE /webhooks/{id}       Remove a webhook
```

Webhook registration:

```json
{
  "url": "http://host.docker.internal:7777/api/sessions/flag?name=my-session",
  "events": ["flag", "flag_clear"],
  "headers": {"Authorization": "Bearer <token>"},
  "timeout_ms": 3000
}
```

### Webhook dispatch payload

When an event fires, Gatepost POSTs to each matching webhook URL:

```json
{
  "session_id": "abc123",
  "session_name": "my-session",
  "ts": "2025-07-14T14:32:01Z",
  "event": {
    "type": "flag",
    "reason": "Claude Done",
    "source": "agent",
    "metadata": {}
  }
}
```

For policy-engine-generated events (escalation, deny threshold, etc.):

```json
{
  "session_id": "abc123",
  "session_name": "my-session",
  "ts": "2025-07-14T14:32:06Z",
  "event": {
    "type": "escalation",
    "reason": "unknown host some-api.com denied 3 times",
    "source": "policy-engine",
    "metadata": {
      "host": "some-api.com",
      "deny_count": 3,
      "audit_seq": [12, 15, 18]
    }
  }
}
```

### Dispatch behavior

- Fire-and-forget with configurable timeout per webhook (default 3s)
- Failed webhooks are logged to the audit log but do not block the agent
- No retries in v1 (keep simple; orchestrators can poll audit log as fallback)
- Webhook calls go through the proxy container's egress network (it already has internet access)
- Webhooks are never called for internal/routine events — only for things an orchestrator would act on

### Internal event sources

Beyond agent-submitted events, the policy engine itself can fire webhook events:

| Source | Event type | Trigger |
|--------|-----------|----------|
| Agent via `/agent/event` | `flag`, `flag_clear`, `status`, `custom` | Agent calls the endpoint |
| Policy engine | `escalation` | `unknown_action: escalate` (future Phase 3) |
| Policy engine | `deny_threshold` | N denies for same host in a window (configurable) |
| Phase manager | `phase_changed` | Phase transition completed |

### Changes to `cmd/gatepost-policy/main.go`

Add a webhook dispatcher and the agent-facing listener:

```go
// Webhook dispatcher
type WebhookDispatcher struct {
    mu       sync.RWMutex
    hooks    []WebhookConfig
    client   *http.Client
    logger   *slog.Logger
    session  SessionInfo  // session_id + session_name for payloads
}

type WebhookConfig struct {
    ID        string            `json:"id"`
    URL       string            `json:"url"`
    Events    []string          `json:"events"`    // ["flag", "*", ...]
    Headers   map[string]string `json:"headers"`
    TimeoutMs int               `json:"timeout_ms"`
}

func (d *WebhookDispatcher) Fire(event Event) {
    // Match event.Type against each hook's Events list ("*" matches all)
    // POST to URL with configured headers, with per-hook timeout
    // Log failures, don't block
}
```

```go
// Agent-facing listener
agentAddr := os.Getenv("GATEPOST_AGENT_ADDR")
if agentAddr == "" {
    agentAddr = "0.0.0.0:18083"
}

agentMux := http.NewServeMux()
agentMux.HandleFunc("GET /agent/healthz", handleHealthz)
agentMux.HandleFunc("POST /agent/event", handleAgentEvent(logger, dispatcher))
```

```go
func handleAgentEvent(logger *slog.Logger, dispatcher *WebhookDispatcher) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var body struct {
            Type     string         `json:"type"`
            Reason   string         `json:"reason"`
            Metadata map[string]any `json:"metadata"`
        }
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            http.Error(w, "bad request", http.StatusBadRequest)
            return
        }
        // Validate type is in the allowed set
        if !isValidAgentEventType(body.Type) {
            http.Error(w, "invalid event type", http.StatusBadRequest)
            return
        }
        logger.Info("agent event", "type", body.Type, "reason", body.Reason)
        dispatcher.Fire(Event{
            Type:     body.Type,
            Reason:   body.Reason,
            Source:   "agent",
            Metadata: body.Metadata,
        })
        w.WriteHeader(http.StatusAccepted)
    }
}
```

### Changes to `compose/session.yml`

```yaml
services:
  proxy:
    environment:
      GATEPOST_AGENT_ADDR: "0.0.0.0:18083"
      GATEPOST_WEBHOOKS: ${GATEPOST_WEBHOOKS:-[]}

  agent:
    environment:
      GATEPOST_AGENT_URL: http://proxy:18083
```

### Changes to `docker/proxy/Dockerfile`

```dockerfile
EXPOSE 8080 18083
```

### How consumers use it

**DevX** registers a webhook at session creation that maps `flag`/`flag_clear` events to `devx session flag`:

```bash
GATEPOST_WEBHOOKS='[{
  "url": "http://host.docker.internal:7777/api/sessions/flag?name='$SESSION_NAME'",
  "events": ["flag", "flag_clear"],
  "headers": {"Authorization": "Bearer '$WEB_TOKEN'"},
  "timeout_ms": 3000
}]'
```

**Hermes/OpenClaw** registers a webhook that catches everything:

```bash
GATEPOST_WEBHOOKS='[{
  "url": "http://host.docker.internal:9090/hermes/agent-event",
  "events": ["*"],
  "timeout_ms": 5000
}]'
```

**Standalone CLI** registers nothing — events are logged to the audit log and visible via `gatepost-verify` or log inspection.

### What the agent sees

Inside the agent container:

```bash
# Signal completion
curl -s -X POST http://proxy:18083/agent/event \
  -H 'Content-Type: application/json' \
  -d '{"type": "flag", "reason": "Claude Done"}'

# Clear flag
curl -s -X POST http://proxy:18083/agent/event \
  -H 'Content-Type: application/json' \
  -d '{"type": "flag_clear"}'

# Status update
curl -s -X POST http://proxy:18083/agent/event \
  -H 'Content-Type: application/json' \
  -d '{"type": "status", "reason": "running tests"}'
```

Orchestrators can install a small wrapper script in the agent image:

```bash
#!/bin/sh
# /usr/local/bin/gatepost-event
TYPE="${1:-flag}"
shift
curl -s -X POST "${GATEPOST_AGENT_URL}/agent/event" \
  -H 'Content-Type: application/json' \
  -d "{\"type\": \"$TYPE\", \"reason\": \"$*\"}" >/dev/null 2>&1
```

Usage: `gatepost-event flag "Claude Done"` or `gatepost-event status "installing deps"`

---

## 3. Control Plane Must Not Be Reachable from Agent

### Problem

The control plane listener binds `0.0.0.0:18082`. Since the proxy container is on `session-internal` (shared with the agent), the agent can reach `proxy:18082` and:
- Read the current phase and policy rules (`GET /phase`, `GET /rules`)
- Modify the phase (`POST /phase`)
- Add/remove rules (`PUT /rules`, `DELETE /rules/{id}`)
- List secret names (`GET /secrets`)
- Register new secrets (`POST /secrets`)

This defeats the purpose of policy isolation.

### Changes

**Bind the control plane to the egress network interface only**, not all interfaces:

Option A — **Bind to 127.0.0.1 and rely on host port publishing:**

```go
controlAddr := "127.0.0.1:18082"  // NOT 0.0.0.0
```

Since Docker publishes `127.0.0.1:<host-port>:18082`, the port forwarding still works for the host. But the agent can't reach `127.0.0.1:18082` inside the proxy container because the agent is a different container — it would try `proxy:18082` which hits the proxy container's external interfaces.

Wait — this doesn't work cleanly. If the control plane binds `127.0.0.1` inside the proxy container, Docker's port publishing still maps the host port to the container's `127.0.0.1:18082`. But the agent reaching `proxy:18082` would hit the proxy container's Docker-assigned IP, not `127.0.0.1` — so the connection would be refused. This **is** the desired behavior.

**Recommended change:**

```go
// cmd/gatepost-policy/main.go
controlAddr := os.Getenv("GATEPOST_CONTROL_ADDR")
if controlAddr == "" {
    controlAddr = "127.0.0.1:18082"  // Changed from 0.0.0.0:18082
}
```

```yaml
# compose/session.yml
services:
  proxy:
    environment:
      # Control plane on localhost only — not reachable from agent.
      # Docker port publishing still maps host:<random>:18082 correctly.
      GATEPOST_CONTROL_ADDR: "127.0.0.1:18082"
```

**Verification test to add to `scripts/gatepost-verify`:**

```bash
# Control plane must NOT be reachable from agent
docker exec "$AGENT" curl -fsS --connect-timeout 2 http://proxy:18082/healthz
# Expected: connection refused or timeout (exit nonzero)
```

---

## 4. Compose Overlay Support for DevX

### Problem

DevX needs to extend the base `compose/session.yml` with:
- Port publishing on the agent container (for Caddy/Cloudflare)
- Extra environment variables (SESSION_NAME, *_PORT, *_HOST)
- DevX labels
- Policy file mount (proxy only)
- Flag callback URL
- `restart: unless-stopped`

### Changes

No changes to `compose/session.yml` are strictly required — Docker Compose's `-f base.yml -f override.yml` merge works. But a few things make overlay easier:

**Document the overlay contract:** Which env vars and volumes are extension points. Add a comment block to `compose/session.yml`:

```yaml
# Extension points for orchestrators (DevX, Hermes, etc.):
#
# Agent ports:     Add `ports:` to the agent service in your override.
# Agent env:       Add `environment:` entries to the agent service.
# Agent labels:    Add `labels:` entries to the agent service.
# Policy file:     Mount to proxy at /etc/gatepost/policy.yaml:ro
# Webhooks:        Set GATEPOST_WEBHOOKS env on proxy.
# Restart policy:  Add `restart:` to both services.
# Agent image:     Set GATEPOST_AGENT_IMAGE.
```

**Add `restart` as an overlay-friendly default:**

Currently the agent runs `command: sleep infinity` with no restart policy. DevX will add `restart: unless-stopped` in its override. No change needed in the base template, but document that this is expected.

---

## 5. Policy Engine Escalation via Webhooks (Future)

### Problem

When the policy engine encounters an unknown host and `unknown_action: evaluate`, it currently makes a deterministic allow/deny decision. In Phase 3 (LLM evaluation), it may want to escalate to the human operator.

With the generic webhook system (section 2), this is straightforward — the policy engine fires an `escalation` event, and whatever orchestrator is listening gets it through its registered webhook.

### Design (not needed for initial integration, but plan the hook)

Add an `escalate` decision type to the policy engine:

```go
const DecisionEscalate = "escalate"
```

When the engine returns `escalate`, the mitmproxy addon:
1. Denies the request with 503 (retry-friendly)
2. Writes an escalation entry to the audit log
3. The policy service fires an `escalation` webhook event with host/path/deny context

The orchestrator receives the webhook and does whatever makes sense:
- DevX: sets an attention flag, shows in TUI
- Hermes: pauses the agent loop, notifies the operator
- Standalone: logged to audit, visible via `gatepost-verify`

Gatepost doesn't need to know what the orchestrator does with the event.

### Changes needed later

- `internal/policy/policy.go`: Add `DecisionEscalate` constant
- `internal/policy/policy.go`: `evaluateDeterministic()` returns `escalate` instead of `deny` for ambiguous cases (when configured)
- `proxy/mitmproxy/gatepost_addon.py`: Handle `decision: "escalate"` — deny with 503, fire webhook
- Policy YAML schema: `unknown_action: escalate` option
- Also consider a `deny_threshold` webhook: fire after N denies for the same host in a time window (configurable)

No code changes needed now — the webhook dispatch infrastructure from section 2 is the only prerequisite.

---

## Summary of Changes

| Area | File(s) | Change | Priority |
|------|---------|--------|----------|
| Policy isolation | `compose/session.yml` | Remove workspace mount from proxy; add policy-only volume | **Must have** |
| Policy isolation | `scripts/pi-session-up` | Mount policy from host root, not workspace | **Must have** |
| Policy isolation | `scripts/gatepost-up` | Accept `--policy` flag, mount into proxy only | **Must have** |
| Control plane security | `cmd/gatepost-policy/main.go` | Bind control plane to `127.0.0.1:18082` | **Must have** |
| Control plane security | `compose/session.yml` | Set `GATEPOST_CONTROL_ADDR: 127.0.0.1:18082` | **Must have** |
| Control plane security | `scripts/gatepost-verify` | Add test: agent can't reach control plane | **Must have** |
| Agent API + webhooks | `cmd/gatepost-policy/main.go` | Add agent-facing listener on `0.0.0.0:18083` | **Must have** |
| Agent API + webhooks | `cmd/gatepost-policy/main.go` | Add `POST /agent/event` handler | **Must have** |
| Agent API + webhooks | `cmd/gatepost-policy/main.go` | Add `WebhookDispatcher` — load from `GATEPOST_WEBHOOKS` env, fire on events | **Must have** |
| Agent API + webhooks | `cmd/gatepost-policy/main.go` | Add webhook CRUD on control plane (`GET/POST/DELETE /webhooks`) | Nice to have |
| Agent API + webhooks | `compose/session.yml` | Add `GATEPOST_AGENT_ADDR`, `GATEPOST_WEBHOOKS`, `GATEPOST_AGENT_URL` | **Must have** |
| Agent API + webhooks | `docker/proxy/Dockerfile` | Expose port 18083 | **Must have** |
| Compose overlay | `compose/session.yml` | Document extension points for orchestrators | Nice to have |
| Escalation | `internal/policy/`, addon, YAML schema | `DecisionEscalate` type + flag integration | Future (Phase 3) |
