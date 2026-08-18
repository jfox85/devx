# DevX Web UI Redesign Studies

Three independent GPT-5.6 Sol (high reasoning) design studies explore the DevX left navigation, top chrome, and session-status presentation. They are interactive, self-contained HTML prototypes rather than production replacements.

## 1. Quiet Workspace

**Open:** `quiet-workspace/index.html`

A restrained, Codex-influenced shell with warm neutral chrome, a calm project/session tree, a continuous global bar, and a top-right Environment inspector.

Best ideas to carry forward:

- Clearest overall hierarchy and strongest default direction.
- Attention separated from the normal project tree.
- Session actions visually recede until needed.
- Environment summary provides branch, worktree, target, routes, windows, and artifacts in one place.
- Mobile uses an off-canvas session drawer and status sheet rather than shrinking desktop.

## 2. Operator Console

**Open:** `operator-console/index.html`

An information-dense command center with a narrow utility rail, fleet summary, structured session navigator, status ribbon, and live-operations HUD.

Best ideas to carry forward:

- Fastest scanning for large numbers of concurrent sessions.
- Branch, recency, target, attention, and artifact context in each row.
- Useful two-level distinction between global navigation and session navigation.
- Compact status ribbon works better than a large always-open status card.

## 3. Canvas + Dock

**Open:** `canvas-dock/index.html`

A terminal-first spatial design with an inset collapsible project dock, consolidated floating workspace chrome, layered status card, and mobile bottom-sheet switching.

Best ideas to carry forward:

- Terminal receives the most visual authority.
- Session identity, tmux windows, and actions form one coherent toolbar.
- Collapsible dock creates more room without eliminating project context.
- Mobile session sheet is the strongest switching concept of the three.

## Recommended synthesis

Use **Quiet Workspace as the visual and information-architecture base**, then combine:

1. Operator Console's richer session-row scanability and compact status ribbon.
2. Canvas + Dock's consolidated session/window toolbar and mobile session sheet.
3. A status inspector that is **closed by default**, truthful, and dockable where space permits. It should never silently cover active terminal output.
4. A responsive breakpoint near the current 1024px behavior, with an explicit tablet layout rather than preserving a wide desktop rail too long.

The next iteration should preserve all existing DevX behavior: stale review/cleanup, rename/color/delete, routes/logs, quick switching, terminal output view, artifact creation/insertion, image handling, split modes, mobile composer/soft keys, share flow, toasts, iframe focus/fit, and loading/error/empty states.

## Prototype limitations

Independent reviews found no critical issues, but all three are chrome studies and intentionally use static data. They are not implementation specifications. Before production work:

- Remove or define any unsupported telemetry such as exact runtime, route health, ahead counts, or global inbox destinations.
- Raise small-text contrast and mobile target sizes.
- Implement proper dialog/sheet focus management and hidden-state semantics.
- Validate intermediate widths, long names, large fleets, virtual keyboards, terminal focus/fit, and every existing action path.
- Keep Environment/status separate from the full Artifacts surface.

