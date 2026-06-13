# DevX Desktop Control Deck Implementation Plan

Date: 2026-06-11
Status: revised after architecture/security/simplicity/UX/scope review

## Context

DevX currently provides a browser-based session manager with:

- DevX-managed Git worktree sessions
- tmux sessions and panes
- ttyd-backed in-browser terminal access
- Svelte web UI
- artifacts attached to sessions
- service links through Caddy
- attention flags and SSE notifications
- mobile access through browser/PWA-style workflows

The current web UI is flexible and harness-neutral, but the terminal remains the dominant interaction surface. Pain points include:

- Lag when switching sessions while ttyd/tmux/xterm reconnects.
- Session/window/pane state sometimes needing rehydration.
- Browser-tab context competing with other browser work.
- Mobile and speech-to-text input problems when editing inside a terminal.
- Desire for a more native desktop feel without tying DevX to Claude, Codex, OpenCode, Pi, or any specific model/provider.

Guiding principle:

> DevX remains the environment/worktree/session manager. The desktop app becomes the native control plane. The terminal remains available, but stops being the only UI.

## MVP Discipline

The MVP should solve two concrete problems before adding broader control-plane features:

1. **Faster session switching** without changing the core tmux/ttyd architecture.
2. **Reliable long-form input** through a normal composer outside the terminal.

The first production slice should not attempt to build the full desktop/control-plane vision. It should validate the core UX improvements in the existing web UI, then package the same UI in a thin desktop shell.

### Lean MVP

1. Add a single-session terminal prewarm/status backend.
2. Prewarm only the active session and hovered/focused session under a global cap.
3. Add measurable switch-latency instrumentation.
4. Add a multiline composer with per-session drafts and two send modes:
   - paste only
   - paste and submit
5. Package the existing web UI in a minimal desktop shell after the browser path proves the improvements.

### Explicitly Out of MVP Scope

- Batch `prewarm-recent` endpoint.
- Notification-triggered prewarm.
- Iframe keep-alive pool as default production behavior before a spike proves it reliable.
- Dock badges, menu-bar app, and multiple global shortcut schemes.
- Full artifact browser redesign.
- Embedded service preview pane.
- Harness adapter/plugin architecture.
- Adapter-owned send behavior.
- Persistent prompt history by default.

---

## Security Invariants

These requirements apply across all phases:

1. **Local-only control surface**
   - DevX web remains loopback-only unless an explicit, authenticated remote-access mode is configured.
   - Desktop mode should prefer an in-process server or per-launch random loopback port/Unix socket.

2. **Ephemeral desktop auth**
   - Desktop mode should use an ephemeral per-launch auth token kept in memory only.
   - Never pass tokens via query string, CLI args, logs, notifications, or localStorage.
   - If reusing a daemon, verify it with a challenge/response nonce before trusting it.

3. **Privileged app isolation**
   - The privileged desktop WebView loads only the DevX app origin.
   - External links open outside the privileged WebView.
   - Future service/artifact previews must run in an isolated WebView/iframe partition with no shared cookies/storage and no native bindings.

4. **API protection**
   - All `/api/terminal/*` endpoints require authentication, including read-only status endpoints.
   - All state-changing endpoints require auth and origin/CSRF validation.
   - Cross-origin POSTs to prewarm/send endpoints must be rejected.

5. **No automatic terminal send**
   - DevX must never auto-send text from notifications, artifacts, service previews, or harness adapters.
   - `paste and submit` requires an explicit user gesture.

6. **Safe input sending**
   - Send endpoints enforce message size limits and rate limits.
   - Send endpoints do not log message bodies.
   - tmux paste uses named per-request buffers and deletes them after paste.
   - MVP supports only the active pane target; clients cannot specify arbitrary tmux targets.

7. **Redacted notifications and metadata**
   - Native notifications use generic/redacted content by default.
   - Status APIs expose coarse state only; no ports, PIDs, command lines, full paths, environment variables, raw terminal snapshots, or raw prompt bodies.

---

## Phase 0 — Architecture Boundaries and Validation Spikes

Goal: reduce implementation risk before adding stateful desktop/session features.

### 0A. Backend Terminal Service Boundary

Do not add prewarm/status/send behavior directly into `web/server.go`, `web/api.go`, or `web/ttyd.go` handlers. First introduce a small terminal lifecycle service boundary.

Possible package/file shape:

```text
web/terminal_service.go
web/terminal_service_test.go
```

Initial responsibilities:

```go
type terminalService struct { ... }

func (s *terminalService) EnsureReady(sessionName string, reason terminalStartReason) (TerminalStatus, error)
func (s *terminalService) Status(sessionName string) TerminalStatus
func (s *terminalService) ProxyTarget(r *http.Request) (sessionName string, port int, err error)
func (s *terminalService) SendInput(sessionName string, input TerminalInput) error
```

The service should own:

- DevX session validation before terminal start.
- tmux restoration before ttyd start.
- ttyd manager interaction.
- rate/cap decisions for prewarm.
- status redaction.
- test seams for command execution, port allocation, timers, and clocks.

Wiring decision for MVP:

- `Server` owns a `terminal *terminalService` field.
- `New()` constructs `terminalService` with the existing `ttydManager` dependency.
- Terminal-related routes are registered as `Server` methods so they can use `s.terminal` directly.
- Avoid package-level globals and avoid adding new terminal lifecycle logic to package-level API handlers.

HTTP handlers should remain thin transport adapters.

Add a shared write-request guard before Milestone 1 endpoints:

- central helper/middleware for auth-required API routes, origin/CSRF validation, request size limits, and rate limiting
- used by both prewarm and send-input routes
- status remains auth-required but does not need CSRF because it is read-only

Acceptance criteria:

- Existing terminal proxy behavior still works.
- Existing tests pass.
- New terminal service unit tests cover validation and status behavior without spawning real ttyd where possible.

### 0B. Frontend Component Boundaries

Avoid growing `Terminal.svelte` further. Before adding composer or cached terminals, split responsibilities.

Target components/modules:

```text
web/app/src/lib/terminal/TerminalFrame.svelte      # iframe/focus/fit lifecycle
web/app/src/lib/terminal/TerminalChrome.svelte     # header/window tabs/actions
web/app/src/lib/terminal/TerminalDeck.svelte       # later cached terminal containers
web/app/src/lib/composer/PromptComposer.svelte     # later multiline composer
web/app/src/lib/stores/sessionUiState.js           # per-session UI state/drafts
web/app/src/lib/host/browser.js                    # browser host capabilities
web/app/src/lib/host/desktop.js                    # desktop host capabilities later
```

Acceptance criteria:

- The existing terminal view behaves the same after decomposition.
- Desktop-specific capabilities have a host abstraction rather than direct Wails checks scattered through components.

### 0C. Iframe Keep-Alive Spike

The iframe keep-alive pool is high-risk because xterm/ttyd/WebView focus and resize behavior can fail invisibly.

Run a throwaway spike before production implementation:

- Keep two ttyd iframes mounted.
- Hide/show them using the intended CSS strategy.
- Validate in both normal browser and Wails WebView.
- Confirm hidden iframes do not steal keyboard focus.
- Confirm xterm `fit()` restores dimensions reliably on show.
- Confirm switching avoids websocket re-handshake.

Go/no-go:

- If reliable, keep iframe pooling as a later optimization.
- If unreliable, rely on prewarm + fast reconnect instead.

### 0D. tmux Paste-Buffer Input Spike

Before building the full composer, manually validate the core send strategy:

1. Dictate a paragraph on mobile.
2. Copy or send it through the proposed composer path.
3. Use `tmux load-buffer` + `tmux paste-buffer` into Pi, Claude Code, Codex, OpenCode, and a plain shell where available.
4. Confirm the harness receives one clean block.
5. Confirm paste+submit behavior is predictable.

Go/no-go:

- If paste-buffer is reliable, implement composer MVP.
- If not, adjust the backend send strategy before building UI polish.

### 0E. Minimal Wails Feasibility Spike

Before embedding the server in-process, validate the simplest desktop shell using the chosen private-server topology:

- Start a private DevX web server for the desktop app launch.
- Load it in a Wails WebView.
- Confirm ttyd iframe works in the WebView.
- Confirm SSE events reach the app.
- Confirm native notifications can be triggered from host abstraction.

Preferred MVP desktop topology:

- Desktop starts its own private DevX web server for the app launch.
- The private server uses a random loopback port or Unix socket and an ephemeral in-memory token generated by the desktop host.
- The desktop host injects authentication for the privileged app WebView; tokens are not stored in localStorage or placed in URLs.
- Attaching to an existing long-lived daemon is explicitly deferred until a dedicated challenge/response attach protocol is designed.
- In-process server embedding can be revisited after service boundaries are stable.

---

## Phase 1 — Faster Session Switching Without Architecture Replacement

Goal: improve session switching while keeping tmux/ttyd/web architecture intact.

### 1A. Single-Session Terminal Prewarm

Add endpoints:

```http
POST /api/terminal/prewarm
{
  "session": "my-session"
}

GET /api/terminal/status?session=my-session
```

Do not add a batch prewarm endpoint for MVP. The client can call single-session prewarm under a global budget.

`prewarm` should reuse terminal-service validation:

- validate session name
- validate DevX session exists
- ensure tmux session exists/restored
- start ttyd if needed
- return redacted status

MVP status response:

```json
{
  "session": "foo",
  "ready": true,
  "running": true,
  "state": "ready",
  "error": ""
}
```

Allowed `state` values:

- `not_started`
- `starting`
- `ready`
- `error`
- `capped`

Do not expose raw port, PID, command line, path, or raw process error.

### 1B. ttyd Manager Metadata and Caps

Extend internal ttyd metadata only as needed:

```go
type ttydInstance struct {
    port       int
    cmd        *exec.Cmd
    conns      int
    timer      *time.Timer
    startedAt  time.Time
    lastUsedAt time.Time
    prewarmed  bool
    lastError  string
}
```

Add config or internal defaults:

```yaml
web_terminal_prewarm_limit: 3
web_terminal_idle_timeout: 10m
web_terminal_prewarm_idle_timeout: 3m
```

Cap behavior:

- Active terminal sessions are never evicted for prewarm.
- If prewarm cap is reached, return `state: capped` rather than silently doing nothing.
- The frontend may retry when another prewarmed session idles out.

Testing requirements:

- unauthenticated status requests are rejected
- status response is redacted and exposes no ports/PIDs/paths/raw errors
- invalid session cannot prewarm
- unknown DevX session cannot prewarm
- prewarm starts ttyd once
- repeated prewarm is idempotent
- cap returns a safe capped response
- idle cleanup removes prewarmed instances
- cross-origin/state-changing requests are rejected

### 1C. Conservative Frontend Prewarm Policy

Prewarm only visible/intentional targets:

- current active session on app load
- session row hover/focus after ~150ms debounce
- maybe most recent session after list load if within cap

Do not prewarm from flag/artifact notifications in MVP.

UI behavior:

- session row can show a subtle terminal readiness indicator if useful
- capped/failure states should not block opening the session normally
- normal click path remains the source of truth

### 1D. Session UI State Cache

Cache lightweight per-session UI state:

```js
sessionUiState = {
  [sessionName]: {
    lastWindowIndex,
    artifactPaneOpen,
    splitMode,
    selectedArtifactID,
    composerDraft,
    lastOpenedAt
  }
}
```

MVP storage:

- UI layout state may use `sessionStorage`.
- Composer drafts are memory-only by default unless explicitly enabled later.

Acceptance criteria:

- Switching back to a session restores visible chrome quickly.
- Draft text survives switching sessions during the same app lifetime.
- Sensitive prompt history is not persisted by default.

### 1E. Performance Instrumentation and Targets

Add lightweight client-side timing around terminal switching:

- session row click time
- iframe load time
- first successful fit/ready signal if available
- prewarm request duration
- cold vs warm path

Initial target budgets:

- Warm/prewarmed terminal visible: under 500ms on local desktop.
- Cold terminal visible: under 2.5s on local desktop when tmux already exists.
- Prewarm cap: default max 3 ttyd instances.
- Cached iframe cap, if later enabled: default max 2 iframes.

Acceptance criteria:

- Metrics are visible in debug logs or development console.
- Plan can be adjusted based on observed cold/warm timings.

---

## Phase 2 — Native Prompt Composer Outside the Terminal

Goal: fix long-form input problems by composing outside the terminal and sending finalized text exactly once.

### 2A. Backend Send Input API

Use harness-neutral terminology:

```http
POST /api/terminal/send-input
{
  "session": "foo",
  "text": "...",
  "submit": true,
  "mode": "paste-buffer"
}
```

MVP restrictions:

- `session` must be a valid DevX session.
- target is always the active pane for that session.
- `mode` supports only `paste-buffer` initially.
- `submit=true` sends Enter after paste.
- request requires auth, origin/CSRF validation, and rate limiting.
- request body has a configured size limit.
- message body is never logged.

Implementation strategy:

```bash
tmux load-buffer -b devx-<request-id> -
tmux paste-buffer -b devx-<request-id> -t <active-pane>
tmux delete-buffer -b devx-<request-id>
# if submit=true:
tmux send-keys -t <active-pane> Enter
```

Acceptance criteria:

- Sends exactly one copy of the text.
- Named tmux buffers are deleted after use.
- Oversized messages are rejected safely.
- Cross-origin POSTs are rejected.
- Submit requires explicit user gesture in the UI.

### 2B. Composer MVP UI

Add `PromptComposer.svelte` outside the terminal iframe.

MVP features:

- multiline textarea
- per-session memory-only draft
- paste only
- paste and submit
- clear draft after successful send
- keyboard shortcut: Cmd/Ctrl+Enter for paste and submit
- clear visual indication of target session

Deferred features:

- persistent prompt history
- image/file attach
- artifact insertion helpers
- service URL insertion helpers
- terminal selection quoting
- review-before-send flow

Mobile requirements:

- textarea is large enough for comfortable editing/dictation
- composer can be expanded/collapsed
- terminal soft-key toolbar remains available
- send button is explicit and hard to tap accidentally

Acceptance criteria:

- Dictated text can be edited in a normal input field.
- Sending does not duplicate text or replay corrections.
- Works with Pi, Claude Code, Codex, OpenCode, and plain shell through the same tmux path.

---

## Phase 3 — Minimal Desktop Shell

Goal: provide native-app feel after the web UI improvements prove useful.

### 3A. Thin Desktop Wrapper

Recommended stack: Wails.

MVP approach:

- `devx desktop` launches a native app window.
- Desktop app starts or connects to a local DevX web server.
- Desktop shell loads the existing Svelte app.
- Desktop host exposes only minimal native capabilities.

Possible layout:

```text
desktop/
  main.go
  bindings.go
```

CLI integration:

```text
cmd/desktop.go
```

MVP desktop capabilities:

- native app window
- start/connect to DevX web
- native notifications through host abstraction
- external links open in system browser

Deferred desktop capabilities:

- dock badge
- menu-bar app
- global shortcuts beyond one quick-open/switch shortcut
- in-process server embedding
- native auto-update

### 3B. Desktop Security Requirements

Desktop mode must satisfy the security invariants above, especially:

- ephemeral in-memory auth token
- privileged WebView origin lock
- no native bindings exposed to untrusted preview content
- no token in query string or persistent storage
- challenge/response if attaching to a daemon

Acceptance criteria:

- Existing browser UI still works without Wails.
- Desktop wrapper does not become required for normal CLI/web use.
- Native notification content is redacted by default.
- WebView cannot navigate privileged app shell to arbitrary external content.

---

## Phase 4 — Session Control Plane Polish

Goal: improve session comprehension and navigation after the core switching/input pains are addressed.

This phase should start only after validating Phases 1–3. If faster switching and composer solve the main pain, this phase can be scheduled as incremental polish rather than required MVP work.

### 4A. Minimal Session Row Enrichment

Keep early sidebar changes limited to data already cheap or available:

- session name
- project
- branch
- dirty yes/no
- attention yes/no
- terminal ready/running coarse status

Defer:

- harness guess
- artifact count
- service count
- last activity
- detailed git stats

### 4B. Split Session Summary Enrichment

Avoid one always-hot monolithic `/api/sessions/summary` that fans out into git, tmux, artifacts, services, and harness detection on every poll.

Prefer either:

```http
GET /api/sessions/summary
GET /api/sessions/summary?include=git,terminal
GET /api/sessions/{name}/enrichment?include=artifacts,services
```

or cached enrichment with freshness metadata.

Possible base response:

```json
{
  "name": "foo",
  "project": "devx",
  "branch": "feature/foo",
  "dirty": true,
  "attention": true,
  "terminal_state": "ready"
}
```

Acceptance criteria:

- Sidebar remains fast.
- Expensive metadata is cached, lazy, or explicitly requested.
- Failure to compute git/artifact/service enrichment does not break session switching.

### 4C. Quick Switcher

Add one quick switcher after the summary data is reliable.

Search by:

- session name
- project
- branch
- attention state
- dirty state

Initial actions:

- open session
- copy path
- open in editor if already supported

Defer artifact/service search until those data sources are intentionally added.

---

## Future Follow-Ups

These are intentionally not part of the MVP plan.

### Artifact-First Workflow

Potential features:

- recent artifacts per session
- global artifact search
- pin/favorite artifacts
- Markdown/PDF/image/log preview improvements
- send artifact path to composer
- attach artifact to prompt
- open artifact in external app
- focus artifact when session opens

### Service Preview Pane

Potential features:

- open service URL in isolated embedded preview
- open externally
- copy URL
- show health/last response
- screenshot artifact capture

Security requirement: service previews must be isolated from privileged DevX APIs and native bindings.

### Harness Adapters

Adapters remain future exploration. DevX must stay useful when adapter detection fails.

Initial adapter scope, if pursued:

- read-only detect/status only
- no adapter-owned lifecycle
- no adapter-owned send path
- no repo-auto-loaded adapters
- explicit install/enable for privileged behavior

Possible built-in detections later:

- Pi
- Claude Code
- Codex
- OpenCode
- generic terminal

---

## Proposed Delivery Order

### Milestone 0 — Boundaries and Spikes

Deliver:

- terminal service boundary
- frontend terminal component split
- host abstraction
- iframe keep-alive spike result
- tmux paste-buffer manual validation
- minimal Wails feasibility result

### Milestone 1 — Prewarm and Status

Likely files:

- `web/terminal_service.go`
- `web/ttyd.go`
- `web/server.go`
- `web/api.go`
- `web/ttyd_test.go`
- `web/api_test.go`

Deliver:

- single-session prewarm endpoint
- redacted status endpoint
- prewarm cap and cleanup policy
- latency instrumentation
- security tests for auth/origin/rate/metadata redaction

### Milestone 2 — Composer MVP

Likely files:

- `web/terminal_service.go`
- `web/api.go`
- `web/app/src/lib/composer/PromptComposer.svelte`
- `web/app/src/lib/stores/sessionUiState.js`
- `Terminal.svelte` or extracted terminal container integration

Deliver:

- `POST /api/terminal/send-input`
- named tmux buffer send implementation
- multiline composer
- memory-only per-session drafts
- paste-only and paste+submit modes

### Milestone 3 — Desktop Wrapper MVP

Likely files:

- new `desktop/`
- `Makefile`
- new `cmd/desktop.go`
- frontend host abstraction integration

Deliver:

- `devx desktop`
- native window
- native notifications
- secure start/connect to local web server

### Milestone 4 — Optional Switching Enhancements

Only after spike success and measured need.

Deliver if justified:

- production `TerminalDeck.svelte`
- capped iframe keep-alive pool
- LRU eviction
- focus/fit regression tests or manual QA checklist

### Milestone 5 — Control Plane Polish

Deliver incrementally:

- cheap session row enrichment
- split/lazy summary enrichment
- quick switcher

---

## Readiness Checklist Before Implementation

Before starting Milestone 1:

- [ ] Confirm lean MVP scope with user.
- [ ] Record baseline cold/warm terminal switch timings in current web UI.
- [ ] Decide initial prewarm cap and idle timeout defaults.
- [ ] Complete tmux paste-buffer manual validation.
- [ ] Complete minimal Wails WebView + ttyd feasibility check using the chosen private-server desktop topology.
- [ ] Decide whether iframe keep-alive is in or out based on spike result.
- [ ] Add security test plan for new state-changing endpoints.

Before shipping MVP:

- [ ] `gofmt -w .`
- [ ] `go vet ./...`
- [ ] `golangci-lint run --timeout=5m` if available
- [ ] `go test -v -race ./...`
- [ ] `go mod tidy`
- [ ] frontend build/test for `web/app`
- [ ] manual browser QA
- [ ] manual desktop WebView QA if desktop wrapper is included
- [ ] manual mobile composer QA
