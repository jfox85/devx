# DevX Operator Console

An information-dense shell for operators who keep several projects and sessions in motion at once. The design deliberately departs from chat-product minimalism: it uses a narrow utility rail, a persistent structured session queue, a compact status ribbon, and an operations HUD while keeping the terminal as the dominant surface.

## Hierarchy

1. **Utility rail** — stable destinations and attention count without consuming session-list width.
2. **Session navigator** — filter, fleet summary, project grouping, branch, recency, target, attention, git/worktree state, and artifacts.
3. **Session chrome** — project/branch context, live state, terminal windows, and the existing artifact/split/composer controls.
4. **Terminal** — primary work surface.
5. **Live operations HUD** — concise session health, runtime, worktree changes, target, attention queue, and known routes; it is operational context, not an analytics dashboard.

## Interactions

- Filter sessions by title, project, or branch; `/` focuses the filter.
- Collapse project groups and select sessions to update active-session chrome.
- Switch terminal windows.
- Show/hide the operations HUD.
- Toggle an artifact panel and composer; `Cmd/Ctrl+K` opens the composer.
- Mobile bottom navigation opens the session sheet or artifact surface.

## Responsive behavior

At desktop sizes, the utility rail and session navigator remain visible while the top-right HUD floats over unused terminal space. Below 720px, the rail becomes a four-item bottom navigation, the navigator becomes a full-height off-canvas session sheet, status metadata is reduced to a horizontally useful ribbon, and the HUD becomes a compact bottom operations card. Terminal controls prioritize split and compose in the mobile window bar.

## Tradeoffs

The shell favors scan speed and status density over a quiet, spacious presentation. Color is never the only status signal: dots are paired with labels, counts, and symbols. The HUD intentionally repeats a small subset of the ribbon because it provides a tap-friendly mobile summary and a richer desktop route view. Production implementation should preserve the current keyboard navigation and live SSE semantics.

## Intentionally unchanged

This prototype does not add backend features. It retains DevX projects, sessions, attention, git/worktree state, target types, routes, artifacts, terminal windows, composer, artifacts, and split controls. Terminal behavior, session lifecycle actions, target management, and route generation are intentionally unchanged.
