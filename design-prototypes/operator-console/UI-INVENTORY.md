# Operator Console v2 UI inventory

This inventory maps current production shell behavior to its proposed v2 location. Items in the first table are existing capabilities, not new product scope.

| Current capability | V2 location | Prototype treatment |
|---|---|---|
| New session | Navigator header; `Cmd/Ctrl+Shift+C` remains an implementation shortcut | Modal with project, name, and target |
| Search/filter | Navigator search | Name, project, and branch filtering; `/` ignores editable fields |
| Quick switcher | App-level `Cmd/Ctrl+P` | Modal session switcher |
| Project grouping/collapse | Navigator | Collapsible project sections with `aria-expanded` |
| Select/open session | Navigator row | Blue selection; green remains status only |
| Rename session | Session overflow | Dialog concept |
| Session color | Session overflow and small row swatch | Explicitly framed as identifier, not status |
| Delete confirmation/progress | Session overflow | Two-step armed state and removal toast concept |
| Derived status, badges, reasons | Row dot/label; docked Status details; legend | Text labels and reasons; no invented telemetry |
| Target type | Row badge; facts strip; Status details | host/docker/gatepost label and explanation |
| Routes | Status details; row/session action | Addresses only; no health assertion |
| Gatepost logs | Status details or per-session action when enabled | Demonstrated placement; existing URL only |
| Stale summary/review | Amber navigator disclosure | Modal review with clean and repair groups |
| Mark reviewed | Stale review | Per-session action |
| Prune stale clean | Stale review | Confirm then progress concept |
| Repair stale/broken | Stale review | Opens session terminal workflow |
| Terminal windows | Window tablist | Tab semantics plus arrow-key selection |
| Terminal output view | Window toolbar and mobile actions | Copy-friendly modal concept |
| Artifact pane | Facts link, toolbar, Status details, mobile navigation/actions | Session-scoped dock/full pane |
| New artifact | Toolbar, artifact pane, mobile actions | Text artifact modal concept |
| Insert artifact reference | Toolbar and mobile actions | Search/choose modal concept |
| Split modes | Toolbar and mobile actions | terminal, vertical, horizontal, artifacts |
| Desktop compose | Toolbar; `Cmd/Ctrl+K` | Overlay multiline composer with send/paste-only |
| Mobile compose | Docked below terminal | Multiline textarea, paste-only, send |
| Image file attach | Toolbar and mobile actions | Simulated confirmation toast |
| Image paste | App/stage paste handling | Simulated confirmation toast; slash shortcut does not interfere |
| Image drag/drop | Terminal stage | Blue drop target and confirmation toast |
| Mobile action menu | Window bar | Output, image, artifact, and split actions |
| Mobile soft-keybar | Below docked composer | Toggle plus representative existing keys |
| App-level share target | Session overflow; state gallery | Existing token flow placement, not a new sharing model |
| Remote image toast | App-level toast; state gallery | Simulated startup toast and documented state |
| Flag toast | App-level toast; state gallery | Amber fallback notification with reason |
| Loading | State gallery | Stable shell/skeleton guidance |
| API error | State gallery | Inline banner + retry guidance |
| Empty fleet | State gallery | Create-first-session action |
| No filter results | Live navigator state and gallery | Clear-filter action |
| Terminal reconnect | State gallery | Preserve output, reconnect banner, Retry now |
| Mobile session navigation | Bottom dock + modal sheet | Current tab state, focus move/trap/restore, inert background |
| Status legend | Status details | Existing canonical labels and target explanation |

## Data intentionally not claimed

- Session runtime or uptime
- Git ahead/behind counts
- Exact modified/untracked file breakdown
- Route health
- Global Attention, Artifacts, Routes, or Settings destinations
- User profile/avatar

## Future research, not part of this shell prototype

These are post-shell topics tracked in `design-prototypes/HARNESS-RESEARCH.md`; they are not represented as shipped destinations or factual data here:

- Actionable cross-session attention inbox backed by a canonical event model
- Risk-tiered approvals and remote approval threat model
- Rich Git controls (ahead/behind, diff/checkpoint/revert/PR)
- Named repeatable workflows and lifecycle telemetry
- Read-only share links and cross-device deep links
- Mobile approval/dispatch control plane

The implementation sequence is shell fidelity first, then explicitly scoped enhancements after data, security, and interaction contracts exist.
