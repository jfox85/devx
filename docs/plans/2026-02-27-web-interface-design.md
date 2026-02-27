# Design: Web Interface for Mobile-First Remote Access

**Date:** 2026-02-27
**Branch:** jf-add-web

## Overview

Add a web interface to devx that enables full remote control of development sessions from mobile and desktop browsers. The feature has three phases, each independently useful:

1. **Phase 1 — External domain routing + Cloudflare tunnel management**: Makes Caddy service URLs reachable from mobile via a custom domain and Cloudflare tunnel.
2. **Phase 2 — devx web daemon + Svelte SPA**: A browser-based session manager (session list, service links, session create/remove) served by a local Go HTTP server.
3. **Phase 3 — ttyd integration + mobile terminal UX**: In-browser terminal access to tmux sessions, proxied through devx web, with mobile-optimized controls.

---

## Configuration

New top-level keys in `~/.config/devx/config.yaml` (or project-level `.devx/config.yaml`):

```yaml
external_domain: "jon-fox.com"         # enables external routing; no-op if unset
web_secret_token: "..."                 # required to start devx web
web_port: 7777                          # default port for devx web server
web_autostart: false                    # auto-start web daemon when TUI launches
cloudflare_tunnel_config: "~/.cloudflared/config.yaml"  # path to managed file
```

- `external_domain` is the only trigger for external route generation. Existing users with no external domain configured see no behavior change.
- `web_secret_token` is required. `devx web` fails at startup with a clear error if missing.
- `web_autostart` makes the TUI start the web daemon in the background on launch, showing a status indicator in the TUI status bar.

---

## Phase 1: External Domain Routing + Cloudflare Tunnel

### Caddy route changes

When `external_domain` is set, `SyncRoutes` generates routes for both domains per service:

- `[session]-[service].localhost` (existing)
- `[session]-[service].[external_domain]` (new)

The `Routes` map in `Session` continues to store `.localhost` hostnames. The external hostname is derived on the fly by swapping the domain suffix — no schema change required.

### DNS setup (one-time manual)

One wildcard DNS record in Cloudflare:

```
*.jon-fox.com  CNAME  <tunnel-id>.cfargotunnel.com
```

This is created once and never touched again. devx does not manage DNS records.

### Cloudflare tunnel config sync

A new `cloudflare` package manages the cloudflared ingress config file, mirroring the Caddy config management pattern:

- `SyncTunnel(sessions)` — generates ingress rules mapping `[session]-[service].[external_domain]` → `https://[session]-[service].localhost`, plus a final catch-all rule
- Called alongside `SyncRoutes` on session create, session remove, and `devx caddy sync`
- Writes to the path configured in `cloudflare_tunnel_config`

devx does NOT manage the cloudflared daemon, tunnel credentials, or DNS records. Those are one-time setup steps.

### New commands

```
devx cloudflare sync    # regenerate cloudflared config from current sessions
devx cloudflare check   # validate cloudflare setup (see below)
```

`devx cloudflare check` validates:
- `cloudflared` binary is installed
- Tunnel daemon is running
- Config file exists and parses cleanly
- Ingress rules match current active sessions
- DNS: wildcard `*.[external_domain]` resolves to the tunnel

---

## Phase 2: devx web Daemon + Svelte SPA

### CLI commands

```
devx web              # run in foreground (Ctrl-C to stop)
devx web --daemon     # run in background, write PID to ~/.config/devx/web.pid
devx web stop         # stop background daemon
devx web status       # show whether daemon is running and on which port
```

### Authentication

Token-based auth layered on top of Tailscale (network-level gate):

- The SPA presents a login screen on first visit, prompting for the `web_secret_token`
- On success, a session cookie is set; the token is stored in `localStorage` for future visits
- All API calls require `Authorization: Bearer <token>` or a valid session cookie
- Tailscale restricts which devices can reach the server at all; the token is defense-in-depth

### Go HTTP server (`web/` package)

```
web/
  server.go     # HTTP server setup, middleware (auth, CORS, logging), routing
  api.go        # REST endpoints
  ttyd.go       # ttyd lifecycle manager + WebSocket proxy (Phase 3)
  embed.go      # //go:embed dist/*
  app/          # Svelte source
  dist/         # compiled Svelte output (gitignored, embedded at build time)
```

REST API endpoints:

```
GET  /api/sessions              # list all sessions
POST /api/sessions              # create new session (branch, project)
GET  /api/sessions/:name        # session detail
DELETE /api/sessions/:name      # remove session
POST /api/sessions/:name/flag   # set/clear attention flag
GET  /api/health                # server health + caddy/cloudflare status
```

### Svelte SPA (`web/app/`)

Built with Vite + Svelte. `make build` compiles to `web/dist/`, embedded in the Go binary via `//go:embed dist/*`.

Three main views:

**Session List (home)**
- Cards per session: name, branch, service links (both `.localhost` and external domain), attention flag indicator
- "New Session" action: select project → enter branch name → submit
- Mobile-first: large touch targets, responsive card grid, bottom navigation bar

**Session Detail**
- Service links with one-tap open
- Session actions: remove, flag
- "Open Terminal" button → navigates to terminal view (Phase 3)

**Settings / Status**
- devx web server info (port, uptime)
- Caddy health status
- Cloudflare tunnel health status (if configured)

### TUI integration

When `web_autostart: true`, the TUI checks on launch whether the web daemon is running (by checking the PID file) and starts it in the background if not. A small indicator in the TUI status bar shows "web ✓" or "web ✗".

---

## Phase 3: ttyd Integration + Mobile Terminal UX

### ttyd lifecycle

When a session's terminal is opened in the web UI:

1. devx web checks if a ttyd process is already running for that session
2. If not, starts `ttyd -p <random-port> tmux attach -t <session-name>`
3. Tracks the process and port in memory
4. Proxies WebSocket connections: `/terminal/:session/ws` → `ws://localhost:<port>/ws`
5. Stops ttyd after 30s idle (no active WebSocket connections)

From Tailscale/Cloudflare's perspective, there is one external port — all terminal traffic flows through devx web.

### Mobile terminal UX

The terminal view wraps the ttyd xterm.js instance with a mobile control layer:

**Persistent header bar** (always visible):
- Back/home button → returns to session list (does not kill the tmux session)
- Current session name + branch
- Active pane/window indicator

**Pane/window nav bar** (below header):
- tmux windows shown as tappable tabs
- Tapping sends `Ctrl-B <window-number>` to switch windows

**Soft key toolbar** (pinned above the keyboard when focused):
- `Ctrl-C`, `Escape`, `Tab`, arrow keys, `Ctrl-Z`
- Designed for one-thumb reach on mobile

**Session switcher:**
- Tap a session icon in the header to switch to a different session's terminal
- Disconnects current ttyd view, connects to new session's ttyd

### Navigation model

```
Session List
    ↓ tap session
Terminal View
  [← Back]  [session-name / branch]  [win1] [win2] [win3]
  ┌─────────────────────────────────────────────────────┐
  │                   xterm.js (ttyd)                   │
  └─────────────────────────────────────────────────────┘
  [Ctrl-C] [Esc] [Tab] [↑] [↓] [←] [→] [Ctrl-Z]
```

Mirrors the TUI mental model: the session list is "home," you dive into a session, and a single tap returns you without disrupting the running tmux session.

---

## Build Pipeline

The Makefile gains a `web-build` target:

```makefile
web-build:
    cd web/app && npm run build

build: web-build
    go build -ldflags "..." -o bin/devx .
```

The Svelte output in `web/dist/` is gitignored but embedded at build time via `//go:embed`. CI runs `web-build` before `go build`.

---

## Testing Strategy

- **Phase 1**: Unit tests for `cloudflare.SyncTunnel()` (snapshot-style, like Caddy config tests). Integration tests for `devx cloudflare sync` command.
- **Phase 2**: Unit tests for API handlers with mock session store. Manual testing of SPA via browser.
- **Phase 3**: ttyd lifecycle unit tests (start/stop/proxy). Mobile UX validated manually on iOS Safari.
