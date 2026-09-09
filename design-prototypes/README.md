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

## Selected direction

**Operator Console v2 is now the working top-level direction.** The refinement keeps its high-density session scanability while replacing green primary styling with DevX blue/cyan; green is reserved for active, healthy, and successful status.

The fleshed-out prototype now accounts for the existing shell's full capability set through progressive disclosure, including session maintenance, stale review, routes/logs, terminal and artifact actions, split modes, app-level states, tablet navigation, and mobile composer/soft-key controls. See [`operator-console/UI-INVENTORY.md`](operator-console/UI-INVENTORY.md) for the complete placement map.

The top-right status treatment is closed by default, uses existing session data only, and docks without covering terminal output. The responsive structure transitions near 1024px and includes explicit tablet and mobile behavior.

Harness-derived enhancements remain intentionally sequenced after the shell direction in [`HARNESS-RESEARCH.md`](HARNESS-RESEARCH.md).

## Prototype limitations

Independent reviews found no critical issues, but all three are chrome studies and intentionally use static data. They are not implementation specifications. Before production work:

- Remove or define any unsupported telemetry such as exact runtime, route health, ahead counts, or global inbox destinations.
- Raise small-text contrast and mobile target sizes.
- Implement proper dialog/sheet focus management and hidden-state semantics.
- Validate intermediate widths, long names, large fleets, virtual keyboards, terminal focus/fit, and every existing action path.
- Keep Environment/status separate from the full Artifacts surface.
