# Canvas + Dock

A self-contained DevX chrome prototype that makes the active session feel like a focused workspace rather than one half of a fixed two-column application.

## Design thesis

The terminal is the product surface; projects and status are supporting context. On desktop, the project/session list becomes a collapsible inset dock, the session/window/action chrome becomes a single floating segmented bar, and detailed worktree health moves into a layered status card over the terminal canvas. This preserves information density while reducing permanent borders and letting the active session hold visual focus.

## Hierarchy

1. **Active session canvas** — terminal output and composer occupy the largest, darkest uninterrupted surface.
2. **Workspace chrome** — session identity, tmux windows, composer/artifacts/split controls, and status visibility are grouped in one floating bar.
3. **Project dock** — projects remain the primary session grouping, with live, dirty, and attention status visible before selection. The dock collapses to a project rail rather than disappearing.
4. **Layered status** — branch/worktree state, target, window/artifact counts, and routes stay useful without permanently widening the top bar.

## Interactions

- Collapse or expand the desktop project dock.
- Expand/collapse project groups, filter sessions, and switch among realistic sample sessions.
- Switch terminal window tabs; matching desktop/mobile tabs stay synchronized.
- Show/hide the desktop status card.
- Focus and submit the composer; use `/` for session search and `Cmd/Ctrl+K` for composer focus.
- Route and toolbar actions provide lightweight prototype feedback.
- On mobile, open session, status, and workspace-control sheets from thumb-reachable controls.

## Responsive behavior

- **Desktop:** an inset collapsible dock, floating workspace chrome, layered status card, terminal canvas, and centered composer are visible together at 1440×1000.
- **Compact desktop:** toolbar labels shorten before controls are removed.
- **Mobile (390×844):** the sidebar is replaced by a bottom workspace switcher. Session switching and secondary controls become bottom sheets, while a compact session header and horizontally scrollable tmux windows retain context. Compose remains a prominent thumb-reachable action and docked input.

## Tradeoffs

- The status card intentionally overlays low-priority terminal space. It can be dismissed, and its mobile equivalent is a sheet.
- The collapsed dock prioritizes project-level switching over individual-session one-click access; expanding it restores the full list.
- The prototype favors implementable CSS/DOM patterns and does not attempt to emulate the actual ttyd iframe.

## Intentionally unchanged

No backend or production Svelte files are changed. Existing DevX concepts remain intact: project-grouped sessions, attention and git/worktree state, host target, routes, artifacts, tmux windows, composer, artifact controls, split layout, image attachment, terminal output view, and new-session entry.
