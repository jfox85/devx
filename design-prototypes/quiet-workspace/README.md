# Quiet Workspace

A restrained, Codex-inspired shell for DevX that keeps the terminal central while making project/session context and environment state easier to scan.

## Hierarchy

1. **Continuous global bar** — DevX identity, current project/session, quick switcher, notifications, and one Environment control.
2. **Project/session navigation** — attention is separated from the calm project tree; active, running, dirty, and flagged states use small semantic markers instead of dense badges.
3. **Session chrome** — session title and git state sit above terminal windows; existing View, Artifacts, Split, and Compose actions remain available but visually recede until needed.
4. **Terminal workspace** — terminal content remains the primary surface. The composer is a compact, persistent secondary surface.
5. **Environment inspector** — the top-right panel progressively discloses existing branch/worktree, target, routes, terminal windows, and artifact information.

## Interactions

- Toggle projects, select sessions, and switch terminal windows.
- Open/close the Environment inspector from the global bar.
- Artifacts opens the inspector; Split cycles a prototype state; Compose focuses the prompt field.
- The prompt form, quick actions, routes, and session changes provide lightweight feedback.
- Escape closes transient navigation and inspector surfaces.

## Responsive behavior

Desktop uses a stable 276px navigation rail, continuous top bar, full terminal context, and an open floating inspector. At phone width, the terminal becomes the default task-focused view: projects/sessions move into an off-canvas drawer, Environment becomes a bottom sheet, actions collapse to icons, and a three-item bottom navigation exposes Sessions, Terminal, and Artifacts. This is a deliberate mobile mode rather than a scaled-down desktop shell.

## Tradeoffs

- The open desktop inspector overlaps the terminal instead of permanently reducing terminal width; status is immediately useful but remains dismissible.
- The palette is intentionally warm-neutral and typography-led. Status color is reserved for meaning, not decoration.
- The prototype prioritizes shell hierarchy and interaction choreography over emulating a live xterm iframe.

## Intentionally unchanged

No new backend concepts are introduced. Projects, sessions, attention, git/worktree state, host/gatepost/docker targets, routes, artifacts, terminal windows, composer, artifact controls, and split controls retain their current product meaning. Session creation, rename/delete, stale cleanup, terminal transport, and artifact internals are not redesigned here.
