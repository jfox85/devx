# Operator Console v2 UI inventory

This inventory maps current production shell behavior to its proposed v2 location. Items in the first table are existing capabilities, not new product scope.

| Current capability | V2 location | Prototype treatment |
|---|---|---|
| New session | Navigator header; `Cmd/Ctrl+Shift+C` remains an implementation shortcut | Modal with project, name, and target |
| Search/filter | Navigator search | Name, project, and branch filtering; `/` ignores editable fields |
| Quick switcher | App-level `Cmd/Ctrl+P` | Modal session switcher using the complete fixture-selection path |
| Project grouping/collapse | Navigator | Collapsible project sections with `aria-expanded` |
| Select/open session | Navigator row | Blue selection; complete fixture refresh for identity, facts, terminal, Status, routes, artifacts, and Gatepost |
| Rename session | Session overflow | Required input; updates the selected fixture and navigator row |
| Session color | Session overflow and small row swatch | Selectable feedback with `aria-pressed`; identifier, not status |
| Delete confirmation/progress | Session overflow | Two-step preview explicitly retains the static fixture |
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
| New artifact | Toolbar, artifact pane, mobile actions | Required text; adds and renders a scoped fixture artifact |
| Insert artifact reference | Toolbar and mobile actions | Inserts the chosen scoped artifact into the active composer |
| Split modes | Desktop/tablet toolbar and actions | terminal, vertical, horizontal, artifacts; phone action truthfully opens Artifacts as a synchronized destination |
| Desktop compose | Toolbar; `Cmd/Ctrl+K` | Overlay multiline composer with send/paste-only |
| Mobile compose | Docked below terminal | Multiline textarea, paste-only, send |
| Image file attach | Toolbar and mobile actions | Simulated confirmation toast |
| Image paste | App/stage paste handling | Simulated confirmation toast; slash shortcut does not interfere |
| Image drag/drop | Terminal stage | Blue drop target and confirmation toast |
| Mobile action menu | Window bar | Output, image, artifact, and split actions |
| Mobile soft-keybar | Below docked composer | Toggle plus representative existing keys |
| App-level share target | Session overflow; state gallery | Matches an available fixture by exact session name or branch, reports useful not-found feedback, and labels token execution as production-only |
| Remote image toast | App-level toast; state gallery | Closes the compact navigator before focusing reachable preview and dismiss actions |
| Flag toast | App-level toast; state gallery | Closes the compact navigator before focusing reachable navigation and dismiss actions |
| Loading | State gallery | Stable shell/skeleton guidance |
| API error | State gallery | Inline banner + retry guidance |
| Empty fleet | State gallery | Create-first-session action |
| No filter results | Live navigator state and gallery | Clear-filter action |
| Terminal reconnect | State gallery | Preserve output, reconnect banner, Retry now |
| Mobile session navigation | Bottom dock + modal sheet | Closed-sheet accessibility hiding; open-state focus trap/restore and inert background |
| Mobile Status / Artifacts | Bottom dock + full destinations | Mutually exclusive, heading focus entry, obscured-control isolation, Escape restoration |
| Status legend | Status details | Existing canonical labels and target explanation |

## Data intentionally not claimed

- Session runtime, uptime, or unlabeled session age
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
