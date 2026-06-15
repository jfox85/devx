# Session Status Colors Plan

Date: 2026-06-11
Status: draft
Branch/worktree context: `jf-color-refactor`; related cleanup thread: `jf-clean-stale`

## Summary

Session colors should move from user-selected visual identifiers to derived session status indicators. The colored dot is already present in CLI, TUI, and Web, but it is too small and too manually maintained to work as an identity system. It is better used as a compact health/attention signal: what needs action, what is safe to clean up, and what has changed since the user last looked.

This overlaps with the stale branch/worktree cleanup direction: session status should become the shared language for both the session list and cleanup decisions. A stale worktree, a branch with no useful divergence, an idle clean session, and a session with important unseen output should be visibly different before the user chooses to remove anything.

## Related `jf-clean-stale` Direction

The local `jf-clean-stale` branch currently points at the same commit as `main`, and its git worktree entry is marked prunable/missing by `git worktree list`. That makes it a useful concrete example of the cleanup/status problem:

- the session metadata can still reference a session/worktree that no longer exists;
- git can report stale/prunable worktree records separately from DevX metadata;
- a branch can exist without diverging from `main`;
- the user needs a safe, reviewable signal before deleting metadata, branches, routes, or artifacts.

So the status model should not only answer “what color is this session?” It should answer “what state is this session in, and what action is probably next?”

## Current State

Existing surfaces:

- Session metadata: `session/metadata.go`
  - `Color`
  - `DisplayName`
  - `AttentionFlag`, `AttentionReason`, `AttentionSource`, `AttentionTime`
  - `LastAttached`
- Color palette: `session/color.go`
  - `red`, `blue`, `green`, `yellow`, `purple`, `orange`, `pink`, `cyan`
  - `AutoColor(name)` assigns deterministic identity colors
- CLI list: `cmd/session_list.go`
  - renders a colored `●`
  - status text covers tmux/editor/Caddy only
- TUI: `tui/model.go`
  - renders colored `●`
  - renders `🔔` for attention
  - sorts attention sessions first within project
  - already computes git additions/deletions for selected/visible sessions
- Web API/UI:
  - `web/api.go` exposes `color`, `attention_flag`, `artifact_count`, `focused_artifact_id`
  - `web/app/src/lib/SessionList.svelte` renders the colored dot and lets the user click it to cycle manual colors
  - Web currently shows artifact count, but not whether artifacts are new/unseen

Current session metadata suggests manual colors are not carrying enough meaning: many sessions have no explicit color, and attention flags are much more semantically meaningful than color assignments.

## Proposed Status Model

Statuses should be derived with a clear priority order. A session can have many facts, but the primary dot should show the highest-priority status.

| Priority | Status | Color | Suggested marker | Meaning | Primary next action |
|---:|---|---|---|---|---|
| 1 | `attention` | orange/amber | `🔔` or `!` | Explicit attention flag: waiting for input, agent stuck/done, manual flag | Open/review session |
| 2 | `unseen_artifact` | cyan or purple | `◆` / `new` | A new artifact exists that the user has not seen | Open artifact/review output |
| 3 | `broken_or_stale` | red | `⚠` | Missing worktree, stale Caddy routes, invalid metadata, prunable worktree ref | Repair or remove metadata/worktree |
| 4 | `dirty` | yellow | `±`, `+N -M` | Worktree has uncommitted changes | Review/commit/discard before cleanup |
| 5 | `active` | green | `▶` | tmux/editor/process is active or recently attached | Keep/open |
| 6 | `cleanup_candidate` | gray | `🧹` | Clean, stopped, old, no unseen output, no attention | Safe candidate for cleanup flow |
| 7 | `idle` | dim gray | none | Clean but not old enough or not enough evidence to cleanup | No immediate action |

Recommended palette discipline: use fewer colors than the old 8-color identity palette. The goal is fast recognition, not variety.

## Priority Rules

Primary status should be selected in this order:

1. `attention` if `AttentionFlag == true`.
2. `unseen_artifact` if artifact manifest has artifacts newer than the session/user seen marker.
3. `broken_or_stale` if the session path is missing, git worktree metadata is prunable/stale, Caddy route metadata is stale, or metadata is otherwise inconsistent.
4. `dirty` if git status/diff reports uncommitted changes.
5. `active` if tmux exists, editor PID is running, or attach happened recently.
6. `cleanup_candidate` if all cleanup-safe criteria pass.
7. `idle` otherwise.

Secondary badges can still show additional facts. Example: an attention session can also show `+120 -4`, but the dot remains orange because attention wins.

## Unseen Artifact Status

This is worth treating as a top-level status because artifacts are often the agent’s proof-of-work, report, screenshot, review, or handoff. If the user primarily lives in Web, a new artifact should be hard to miss.

Current artifact support has:

- manifest entries with `Created` and `Focus`;
- `artifact_count` and `focused_artifact_id` in Web session responses;
- SSE artifact notifications through `web/artifacts_notify.go`.

Missing piece: a durable “seen” marker.

Possible data model options:

1. **Session-level marker**
   - Add `LastArtifactSeenAt time.Time` or `LastSeenArtifactID string` to `session.Session`.
   - Simpler and probably enough.
   - Status is unseen if any artifact `Created > LastArtifactSeenAt`.
2. **Artifact-level marker**
   - Add `SeenAt` to each artifact entry.
   - More precise, but heavier and more write churn.
3. **Web-local marker**
   - Store seen IDs in browser localStorage.
   - Useful for per-device UX, but not enough for CLI/TUI and not durable across browsers.

Recommendation: use a session-level persisted marker first, with optional browser-local enhancement later.

Suggested behavior:

- Creating/focusing an artifact can set `AttentionFlag` only when explicitly requested, but always updates cached status to `unseen_artifact`.
- Opening the artifact pane or selected artifact in Web marks artifacts seen for that session.
- CLI/TUI could add a command/action later: “mark artifacts seen”.

## Cached / Calculated Status, Not On-Demand Only

Because there can be many sessions, status should be calculated asynchronously and cached instead of doing full git/process/artifact checks every time a list renders.

Proposed model:

```go
type SessionStatusSummary struct {
    Primary        string            `json:"primary"`
    Color          string            `json:"color"`
    Label          string            `json:"label"`
    Badges         []string          `json:"badges,omitempty"`
    Reasons        []string          `json:"reasons,omitempty"`
    Dirty          bool              `json:"dirty"`
    Additions      int               `json:"additions,omitempty"`
    Deletions      int               `json:"deletions,omitempty"`
    UnseenArtifacts int              `json:"unseen_artifacts,omitempty"`
    ArtifactCount  int               `json:"artifact_count,omitempty"`
    WorktreeExists bool              `json:"worktree_exists"`
    TmuxStatus     string            `json:"tmux_status,omitempty"`
    EditorRunning  bool              `json:"editor_running,omitempty"`
    CleanupCandidate bool            `json:"cleanup_candidate"`
    CheckedAt      time.Time         `json:"checked_at"`
}
```

Implementation options:

- Store status summaries in a separate cache file, e.g. `~/.config/devx/session-status.json`.
- Keep canonical session metadata clean; do not write volatile dirty/process state into `sessions.json` on every refresh.
- Recalculate in a bounded worker pool.
- Use TTLs by cost:
  - attention/artifact count: cheap, short TTL or event-driven
  - editor/tmux: cheap-ish, short TTL
  - git dirty stats: medium cost, longer TTL, bounded concurrency
  - branch merged/stale checks: expensive, longer TTL or explicit cleanup scan
- Invalidate on known events:
  - session create/remove/attach
  - artifact add/notify/open
  - flag set/clear
  - Web visibility resume / manual refresh

The Web API should return cached status immediately and optionally trigger background refresh, rather than blocking the session list on all git checks.

## Web UI Story

The Web session list is now the primary surface, so it should drive the product shape.

Recommended Web changes:

- Stop using the dot as a manual color cycler.
- Dot color comes from `session.status.color`.
- Tooltip/title explains the status: e.g. `Needs attention: Pi is waiting for your input`.
- Add compact badges next to the name:
  - `new ◆` for unseen artifacts
  - `+N -M` or `±` for dirty worktrees
  - `⚠` for stale/broken
  - `🧹` for cleanup candidates
- Sort within project by status priority, then last activity/last attached, then name.
- Add filters later:
  - `attention`
  - `new artifacts`
  - `dirty`
  - `stale`
  - `cleanup candidates`
- Artifact pane/open behavior should mark unseen artifacts as seen.

The existing `artifact_count` can stay, but `unseen_artifact_count` is the more actionable value for the list.

## CLI and TUI Story

CLI:

- Extend `devx session list` to include status summary fields.
- Avoid expensive checks by default unless cache is stale.
- Add flags later:
  - `--refresh-status`
  - `--status dirty|attention|stale|cleanup`
  - `--json`

TUI:

- Reuse the same status package/cache as Web.
- Replace `sess.color` with `sess.status.color` for the dot.
- Keep the `🔔` marker for attention, but add `new`/`±`/`⚠`/`🧹` badges.
- Avoid per-render git scans across all sessions; use cache + targeted refresh for selected/visible rows.

## Cleanup Candidate Criteria

A session should only be marked `cleanup_candidate` when it is boring and safe:

Required:

- no attention flag;
- no unseen artifacts;
- worktree exists or stale metadata is understood;
- no uncommitted changes;
- no running editor;
- no tmux session;
- not recently attached;
- branch is merged, equal to base, or explicitly considered disposable.

Potential age thresholds:

- soft candidate: idle > 7 days;
- strong candidate: idle > 14 or 30 days;
- stale metadata candidate: worktree missing/prunable, but still require artifact/archive consideration.

Cleanup status should never auto-delete. It should feed a reviewable cleanup flow.

## Migration / Compatibility

- Keep `Session.Color` in metadata for now.
- Stop auto-assigning identity colors for new sessions once status colors land.
- Keep `devx session color` temporarily, but hide/deprecate it or repurpose it later.
- Do not let manual color override primary status color.
- Existing sessions with colors require no migration; their stored color simply stops driving the primary dot.

## First Implementation Pass

1. Add a shared `sessionstatus` package or similar.
2. Define status constants, colors, priority, and summary struct.
3. Add cached status file and refresh functions with bounded concurrency.
4. Add artifact seen marker to session metadata or a small sidecar.
5. Update Web API to return `status` and `unseen_artifact_count`.
6. Update Web session list dot/badges/sort.
7. Update CLI/TUI to consume the same status summary.
8. Add cleanup-candidate filter/report after the status cache is reliable.

## Open Questions

- Should unseen artifact state be per-user/device or global to the session? Initial recommendation: global session-level marker.
- Should opening a terminal count as seeing artifacts? Initial recommendation: no; opening the artifact pane or artifact itself should mark seen.
- What base branch should cleanup use for “merged/equal/disposable”? Project config may need a default base branch.
- Should manual `Color` become a secondary user tag, or should it disappear entirely from UI?
- Should stale/missing worktrees archive artifacts automatically before metadata removal?
