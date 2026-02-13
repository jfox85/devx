# Caddy Config File Management

Replace API-based Caddy route management with config file generation and `caddy reload`.

## Problem

Routes are currently managed via Caddy's Admin API at runtime. These routes don't survive Caddy restarts (the Caddyfile has no routes), and a catch-all route from the empty `:80 {}` block can end up ordered before session routes, blocking all traffic.

## Solution

A new `SyncRoutes()` function builds the full Caddy JSON config from `sessions.json`, writes it atomically to `~/.config/devx/caddy-config.json`, and runs `caddy reload`. This is called after session create, session remove, and `caddy check --fix`.

## Config Structure

```json
{
  "admin": { "listen": "localhost:2019" },
  "apps": {
    "http": {
      "servers": {
        "devx": {
          "listen": [":80"],
          "routes": [
            {
              "@id": "sess-toneclone-jf-add-mcp-frontend",
              "match": [{"host": ["toneclone-jf-add-mcp-frontend.localhost"]}],
              "handle": [{"handler": "reverse_proxy", "upstreams": [{"dial": "localhost:57895"}]}],
              "terminal": true
            }
          ]
        }
      }
    }
  }
}
```

Session routes are generated in deterministic order. No catch-all route exists, eliminating ordering issues entirely.

## New Function: `SyncRoutes()`

```go
func SyncRoutes(sessions map[string]*SessionInfo) error
```

1. Build full Caddy JSON config with all session routes
2. Write atomically to `~/.config/devx/caddy-config.json` (temp file + rename)
3. Run `caddy reload --config <path>`
4. If Caddy isn't running, start it with `caddy run --config <path>`

## Caller Changes

| Operation | Before | After |
|-----------|--------|-------|
| Session create | `ProvisionSessionRoutes()` per session | `SyncRoutes(allSessions)` |
| Session remove | `DestroySessionRoutes()` per session | `SyncRoutes(allSessions)` |
| `caddy check --fix` | `RepairRoutes()` (reorder + create missing) | `SyncRoutes(allSessions)` |

## Deleted Code

- `CreateRoute`, `CreateRouteWithProject` -- no more per-route API calls
- `DeleteRoute`, `DeleteSessionRoutes` -- no more per-route deletion
- `ReplaceAllRoutes` -- no more bulk API replacement
- `EnsureRoutesArray` -- no null array concern
- `reorderRoutes` -- ordering handled at generation time
- `discoverServerName` -- server name is always `devx`
- `RepairRoutes` -- replaced by `SyncRoutes`
- `ProvisionSessionRoutes`, `ProvisionSessionRoutesWithProject` -- replaced by `SyncRoutes`
- `DestroySessionRoutes` -- replaced by `SyncRoutes`

## Kept Code

- `CheckCaddyConnection` -- health checks
- `GetAllRoutes` -- comparing expected vs actual in `caddy check`
- `NormalizeDNSName`, `SanitizeHostname` -- hostname generation
- `Route`, `RouteMatch`, `RouteHandler`, `RouteUpstream` structs -- used by config generation

## Health Check Simplification

`devx caddy check` compares the generated config against what Caddy is actually running. States:

1. Caddy not running
2. Config matches -- all good
3. Config drifted -- `--fix` calls `SyncRoutes()` to regenerate and reload

Per-route "Blocked" status is eliminated. Routes are either "Active" or "Missing".

## Error Handling

- **Caddy not available**: Session operations still succeed. Config file is written so next Caddy start picks up correct routes.
- **Concurrent creation**: Last writer wins. Both sessions are in `sessions.json` before `SyncRoutes` runs, so the final config is correct.
- **Empty state**: Config generated with zero routes, just the base server block.
- **`disable_caddy: true`**: `SyncRoutes` returns early, no file written.

## File Changes

- **Retired**: `~/.config/devx/Caddyfile` (no longer used)
- **Updated**: `~/.config/devx/caddy-start.sh` (use `caddy-config.json` instead of `Caddyfile`)
- **New**: `~/.config/devx/caddy-config.json` (generated, not checked in)
- **New**: `caddy/config.go` (config generation + `SyncRoutes`)
