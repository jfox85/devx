# Recent Session Activity and Pinning

Status: implemented in `9426f98`, `78515e5`, and `5db7cf0`

## Goal

Make active DevX sessions easy to find across projects, without allowing background work or cosmetic metadata changes to make a session look recently used.

The first release should improve the web UI. Pin data should be shared so the TUI can add parity without another metadata migration.

## Product Decisions

### Define activity narrowly

For this feature, **recent activity means the most recent intentional creation or successful ready/open handoff of a session**. Creation counts because a newly created session is genuinely recent even before its first attach. After creation, only a successful ready/open handoff advances recency.

For CLI/TUI, “successful handoff” means DevX has validated the target, successfully ensured the tmux session is available, and is about to transfer control to the blocking attach process. For web, it means the terminal service reports ready and the intended iframe is still the active loaded frame.

It does not include:

- terminal output;
- prewarming;
- polling;
- git or filesystem changes;
- attention/artifact notifications;
- rename, color, route, review, or other metadata edits.

The API exposes two values:

```text
last_opened_at = LastAttached (nullable)
activity_at    = max(CreatedAt, LastAttached) (nullable only for malformed legacy records)
```

A never-opened session therefore sorts by creation time but is labeled `Created <relative time>`, not “opened.” If both values are absent in legacy metadata, return no activity timestamp, sort the session last, and label it `Never opened`.

Do not use `stale.last_active_at` for this UI. `session/stale.go` currently includes `UpdatedAt` in that value, and `SessionStore.UpdateSession` changes `UpdatedAt` for many operations that are not user activity.

### Use two web views, not an ambiguous within-project sort

Add a compact view control below search:

- **Recent** — pinned sessions first, then all other sessions globally by `activity_at` descending. Show the project as a secondary chip because project headers are absent.
- **Projects** — preserve the current alphabetical project groups and status-priority ordering.

Default new/invalid preferences to **Recent** because finding active sessions is the purpose of the feature. Persist the choice per browser in `localStorage` under a versioned key such as `devx_session_list_view_v1`.

This is preferable to calling a within-project ordering “Recent activity”: with many projects, that would not put the most recently used sessions at the top of the list.

### Pins are global and durable

A pin describes the session, not one browser. Persist `Pinned` on `session.Session` so web and TUI observe the same state.

Pinned sessions form one global section at the top in both Recent and Projects views. A pinned session appears only once and carries a project chip for context. Within the pinned section, order by recent activity and then name.

Pinning must not change the session's activity timestamp or generic `UpdatedAt` timestamp.

## Existing Seams

- `session/metadata.go`
  - `Session.LastAttached` already records CLI/TUI attachment.
  - `SessionStore.RecordAttach` is the existing activity mutation.
  - `SessionStore.UpdateSession` always changes `UpdatedAt`, so it must not be used for pins.
- `cmd/session_attach.go`
  - `runSessionAttach` currently calls `RecordAttach` before target validation/attach can fail; implementation must move this to the successful ensure/handoff boundary.
- `session/tmuxp.go` and `target/`
  - current launch/attach helpers mix “ensure tmux exists” with the blocking attach and can swallow some attach failures; the implementation needs a testable ensure-then-handoff seam.
- `tui/model.go`
  - `attachSession` shells through the CLI attach command, so the corrected CLI lifecycle will also define TUI activity.
  - `loadSessions` currently sorts by project, attention, then name.
- `web/app/src/lib/SessionList.svelte`
  - `selectSession` opens a terminal but does not record activity.
  - The list polls every five seconds and tracks keyboard selection by index, which is unsafe once rows can reorder.
  - Current display ordering is project, status priority, then name.
- `web/app/src/lib/Terminal.svelte`
  - Successful cold and pooled terminal-ready paths are the right places to record a web open.
- `web/app/src/lib/QuickSwitcher.svelte`
  - Empty-query ordering currently follows the name-sorted API response.
- `web/api.go`
  - `sessionResponse` does not expose a purpose-built activity timestamp or pin state.
  - `handleListSessions` currently returns name-sorted sessions.

## Phase 1: Shared Metadata and API

### 1. Add pin state and a clean activity projection

In `session/metadata.go`:

- add `Pinned bool` with JSON tag `pinned,omitempty` to `Session`;
- add an exported helper such as `ActivityAt() (time.Time, bool)` returning the later of `CreatedAt` and `LastAttached` plus whether either timestamp is valid;
- add an idempotent, lock-preserving `SetPinned(name string, pinned bool)` mutation that re-reads the latest store and writes only `Pinned` without changing `UpdatedAt`.

Existing JSON remains backward compatible because the zero value is unpinned.

Tests in `session/metadata_test.go` should prove:

- a newly created but never-opened session returns creation as activity while `LastAttached` remains zero;
- attach wins when later;
- malformed legacy records with neither timestamp report unknown activity;
- pin/unpin persists;
- pinning is idempotent;
- pinning preserves `UpdatedAt` and `LastAttached`;
- concurrent targeted mutations do not clobber a pin.

### 2. Extend the web API

In `web/api.go`, add to `sessionResponse`:

- `ActivityAt *time.Time` with JSON tag `activity_at,omitempty`;
- `LastOpenedAt *time.Time` with JSON tag `last_opened_at,omitempty`;
- `pinned`.

Map zero values explicitly to `nil`; do not serialize Go's year-1 timestamp. Register the stateless pin routes in `web/api.go:registerAPIRoutes`:

- `POST /api/sessions/pin?name=...`;
- `DELETE /api/sessions/pin?name=...`.

Register `POST /api/sessions/activity` in `Server.registerRoutes` because it requires the server-held ttyd receipt state. Web callers must provide a short-lived terminal-ready receipt.

The activity route validates that the receipt belongs to the named session and a successfully connected terminal frame before calling the corrected `RecordAttach`. A name alone or an ordinary ttyd process-ready status is insufficient. The operation is idempotent in meaning even though the timestamp advances. Unknown sessions return 404; malformed/invalid receipts return 400 or 409; persistence failures return 500.

The session-list cache is keyed by the metadata fingerprint, so successful metadata writes naturally produce a new cache key. Add direct route tests in `web/api_test.go` plus authentication coverage through `authMiddleware`. Raw-JSON assertions must cover omitted nullable timestamps and ensure year-1 values never leak onto the wire.

## Phase 2: Web Experience

### 3. Correct attach/readiness lifecycle and record successful opens

Before wiring the UI, correct CLI/TUI recording:

- refactor `cmd/session_attach.go`, the automatic handoff paths in `cmd/session_create.go`, `session/tmuxp.go`, and the relevant `target/` helper so target validation and tmux readiness happen before `RecordAttach`;
- define the timestamp boundary as immediately before transferring control to the blocking attach process, after readiness succeeds;
- stop treating ensure/launch failures as successful activity;
- make the ensure/handoff commands injectable enough to test failed container targets, missing tmux/tmuxp, and ensure failures without launching a real interactive attach;
- test both attach-existing and automatic host/container create handoffs, proving successful handoffs record `LastAttached` and failed readiness does not.

In `web/app/src/api.js`, add a non-blocking `recordSessionActivity(name)` call.

The existing ttyd process status plus iframe `load` event is not sufficient: a cross-origin desktop error document can satisfy both. Add an attempt-specific readiness protocol across `web/app/src/lib/Terminal.svelte`, `web/server.go`, and the terminal proxy:

1. `Terminal.svelte` creates an unguessable open-attempt ID and captures immutable `{name, frame, attempt}` before loading a cold frame.
2. The terminal URL carries that attempt through a proxy-owned path/query value that the server strips before forwarding to ttyd.
3. The proxy records a server-side connection lease for `{session, attempt}` only after both browser upgrade and upstream ttyd WebSocket establishment succeed. The lease remains valid only while that exact proxied connection is live and is invalidated on disconnect. Merely starting a ttyd process or loading an HTTP document does not qualify.
4. An authenticated same-origin readiness endpoint mints a short-lived, session-bound ready receipt from a valid connection lease. The client verifies that `{name, frame, attempt}` is still active and passes the receipt to `recordSessionActivity`.
5. Store the verified attempt identity on a healthy pooled frame. On every later pooled promotion, request a fresh receipt from the still-live connection lease before recording another intentional open; never rely on an expired original receipt.

If propagating an attempt through ttyd's generated WebSocket URL is not feasible, use a same-origin trusted wrapper plus `postMessage` handshake instead. Treat resolving this protocol as an implementation spike/gate; do not fall back to status-plus-load.

Do not record from `prewarmTerminal`, row hover, initial list selection, iframe error/401/500 documents, readiness timeouts, stale A→B switch completions, failed terminal loads, expired receipts without a live lease, or pooled frames whose connection has closed. Test receipt renewal on a pooled promotion after the original receipt TTL as well as rejection after disconnect.

Deduplicate repeated ready events for the same session in one client for a short interval. Activity persistence failure must not prevent terminal use; repeated failures may use the existing non-modal error surface.

Historical web opens cannot be reconstructed. Sessions without a prior CLI/TUI attach initially fall back to creation time and become accurate after their next successful web open.

### 4. Extract deterministic ordering logic

Create a small pure module (for example `web/app/src/lib/sessionOrdering.js`) that provides:

- validated preference loading/saving;
- timestamp parsing with invalid values sorted last;
- deterministic recent comparison (`activity_at` descending, name ascending);
- current project/status ordering;
- partitioning into pinned and unpinned rows.

This keeps comparator behavior testable without coupling it to Svelte rendering. Add Node built-in tests and a `test` script to `web/app/package.json`. Explicitly reject `null`, missing, empty, malformed, and non-finite timestamp inputs before constructing `Date`; test valid and equal timestamps with deterministic name ties.

### 5. Add the Recent / Projects control

In `web/app/src/lib/SessionList.svelte`:

- place a two-option segmented control or native select below search;
- use visible text labels, `aria-label="Session view"`, and clear selected state;
- disable the control during initial loading;
- preserve search when switching views;
- render the global pinned section first;
- in Recent, render one flat globally sorted list with project chips;
- in Projects, preserve current groups and status ordering for unpinned rows, excluding pinned rows because they appear once in the global Pinned section;
- show compact relative activity (`now`, `12m`, `3h`, `4d`) with an absolute `<time datetime>` value/title;
- label rows with no `last_opened_at` but valid creation activity as `Created <relative time>`;
- keep invalid activity rows last and label them `Never opened`.

Relative labels can refresh on a low-frequency timer; they must not trigger API writes or row reordering.

### 6. Add accessible pin interaction

Add `pinSession` and `unpinSession` to `web/app/src/api.js`.

Each row receives a native pin button before destructive actions:

- `aria-pressed={session.pinned}`;
- `Pin <name>` / `Unpin <name>` accessible label;
- always visible when pinned and on mobile;
- visible on hover and `focus-within` when unpinned on desktop;
- at least a 44px mobile target and a visible focus ring;
- optimistic movement with per-row pending state;
- rollback and retryable error on failure;
- polite live-region announcement after success.

Pin interaction must not open, rename, prewarm, or otherwise activate the row.

### 7. Make reordering safe

Replace index-only keyboard identity in `SessionList.svelte` with the selected session name. Derive its current index from `displayOrdered` after sorting/filtering/polling.

Rules:

- keep the same named selection after activity or pin reorders;
- select the first match if filtering hides it;
- select the nearest surviving row after deletion;
- retain focus on the moved pin control after pinning;
- do not auto-scroll the sidebar away from a different control the user is operating.

While touching the row, make the primary open action a native button rather than the current `span role="button" tabindex="-1"`, and reveal secondary actions with `group-focus-within` as well as hover.

### 8. Align the quick switcher

Update `web/app/src/lib/QuickSwitcher.svelte`:

- empty query: pinned first, then activity descending, then name;
- non-empty query: fuzzy relevance remains primary, with pin/activity/name as tie-breakers;
- show a compact pin marker and project alias.

This keeps the fastest navigation path consistent with the sidebar.

## TUI Parity (Implemented)

The TUI parity milestone shipped after the web behavior and activity semantics were validated.

In `tui/model.go`:

- add `pinned` and `activityAt` to `sessionItem`;
- add Recent ordering globally by activity;
- define Projects as the TUI's existing project/attention/name ordering unless a separate shared-status project explicitly adds the web `status.priority` projection;
- render a dedicated global pinned section rather than faking a project alias;
- preserve the cursor by session name before replacing `m.sessions` during periodic reloads;
- use `*` to toggle the selected session's pin (avoids the existing `p` preview binding);
- use an available key such as `s` to switch ordering modes after checking the complete key map;
- render a terminal-width-safe ASCII pin marker rather than relying on emoji width;
- place new bindings in extended help if the normal footer becomes crowded.

Persist the TUI view preference in user-scoped `~/.config/devx/ui-state.json`, defaulting to `recent`, independently of project config and the browser-local preference. `v` switches views and `*` toggles pinning. `lastVisibleSession`, `renderSessionList`, scroll budgets, sorting, pin mutation, and cursor retention are covered in `tui/model_test.go` and `config/ui_state_test.go`.

Do not silently change `devx session list` ordering. A later CLI enhancement may add explicit `--sort recent|name` and a pin marker.

## UX States

- **Loading:** keep search/view chrome visible but disabled; mark the list busy.
- **No sessions:** retain the create-first-session action; omit Pinned.
- **No matches:** quote the query and provide Clear search.
- **Initial error:** distinguish it from a successful empty result and offer Retry.
- **Background refresh error:** retain stale rows/scroll and show a non-repeating retry message.
- **Pin error:** rollback only the attempted row and retain focus.
- **Concurrent clients:** latest successful write wins; five-second polling reconciles clients. Pin-specific SSE is a follow-up, not an MVP requirement.

## Verification

### Automated

- `gofmt -w .`
- `go vet ./...`
- `golangci-lint run --timeout=5m` when available
- `go test -v -race ./...`
- `go mod tidy` and confirm no unintended diff
- `npm test` in `web/app` for pure ordering/wire-value tests
- `npm run test:ui` in `web/app` for deterministic browser interaction tests
- `npm run build` in `web/app`

### Browser/runtime

Add a repeatable Playwright-based `npm run test:ui` harness with deterministic API/session fixtures. It must assert stable selected/focused session identity, exactly-once row rendering, optimistic rollback, stored preference restoration, rapid reorder races, and desktop/mobile keyboard/touch behavior.

Also use browser automation at desktop and mobile widths to inspect and capture proof that:

- Recent is globally ordered across projects;
- Projects preserves current grouping/status behavior;
- preference survives reload;
- pin/unpin moves exactly one row and survives server restart;
- a nonce-correlated successful terminal WebSocket receipt plus active-frame identity advances activity, while prewarm, desktop 401/500/error documents, readiness timeout, and stale rapid-switch completions do not;
- sorting/polling does not move keyboard selection to another session;
- pin, row, search, and view controls are keyboard reachable with visible focus;
- optimistic pin failure rolls back cleanly;
- search, stale review, delete confirmation, routes, logs, colors, and QuickSwitcher still work.

## Acceptance Criteria

1. Recent view globally surfaces the most recently created or successfully readied/opened unpinned sessions, while distinguishing Created from Opened in row copy.
2. Cosmetic metadata changes, notifications, polling, and prewarming do not alter activity ordering.
3. A successfully ensured attach/create CLI/TUI handoff or nonce-correlated terminal WebSocket receipt plus active-frame cold/pooled web open updates the same activity concept; failed target/ensure, iframe 401/500/error, timeout, prewarm, and stale switch completions do not.
4. Pinned sessions are global, durable, shown once, and do not alter activity timestamps.
5. Projects view retains today's project/status organization for unpinned rows; pinned rows intentionally appear once in the global Pinned section.
6. The web preference survives reload and invalid stored values safely fall back to Recent.
7. Poll-driven reordering preserves selection by session identity.
8. Sidebar and QuickSwitcher ordering do not contradict each other.
9. All new controls work by keyboard and at mobile touch sizes without relying on color alone.
10. Existing session metadata remains backward compatible.

## Deferred Ideas

- cross-device/server-side display preferences;
- pin SSE events for immediate multi-client updates;
- user-selectable global Name ordering;
- continuous terminal-input or WebSocket activity tracking;
- CLI sort flags;
- replacing double-click rename with an explicit row action menu.

Continuous process/output activity should not be added casually: it would require write throttling, clearer privacy semantics, and a decision about whether unattended agent output should reorder the user's list.
