# devx web — redesign prototypes

Three self-contained, dark-theme redesign concepts for the devx web interface,
focused on rethinking the **left nav** (session list) and **top nav** (terminal
header). Each is a static, clickable HTML mockup filled with realistic session
data — no build step, no dependencies. Open any file directly in a browser, or
start from `index.html` for the gallery.

| # | Concept | Left nav | Top nav | Vibe |
|---|---------|----------|---------|------|
| 01 | **Aurora** | Airy, rounded project list with pill statuses | Centered global omnibox + breadcrumb path | Spacious modern command deck (periwinkle → aqua) |
| 02 | **Workbench** | Icon activity-rail + collapsible project→session tree | Browser-style session tabs + toolbar + status bar | Dense IDE (graphite + electric blue) |
| 03 | **Cockpit** | Dock of live session tiles with per-session readouts | Telemetry HUD strip (Caddy, ports, tunnels, uptime) | Retro-futuristic control room (amber + cyan, scanlines) |

## Files

- `index.html` — gallery landing page linking to all three
- `01-aurora.html`
- `02-workbench.html`
- `03-cockpit.html`

## Notes

- All three keep the existing dark palette lineage (navy/near-black grounds) but
  each picks a deliberately different accent, type treatment, density, and
  layout so the directions read as genuinely distinct rather than reskins.
- Interactions are wired up for feel: selecting a session, switching window/tabs,
  toggling filters, collapsing tree groups. There is no backend — data is mocked
  to mirror real devx concepts (projects, worktrees, ports, Caddy routes,
  artifacts, tmux windows, attention flags, target types).
- These are prototypes for direction-setting, not production Svelte components.
  Once a direction is chosen, the patterns map onto `App.svelte`,
  `SessionList.svelte`, and the `Terminal.svelte` header.
