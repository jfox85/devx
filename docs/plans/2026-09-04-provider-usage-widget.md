# Provider Usage Widget (Claude / Codex quota in the DevX UI)

Status: implemented in `906e870`, `87e8c36`, `26613e3`, `7806b4f`, `8a999c3`

## Goal

Show remaining subscription allowance for Claude Code and Codex inside DevX web, desktop, and mobile, so you can glance at "how much 5-hour window do I have left" from the session list and drill into weekly / model-scoped windows on demand.

**Data comes from a Redline service running on the same host.** DevX does not touch provider credentials, does not import Redline Go code, and degrades to a calm "Redline unavailable" state when the service isn't running.

## Decisions (from review of v1)

| Question | Decision |
|---|---|
| Data source | Read from local Redline over HTTP (`127.0.0.1:7436`). No Go dependency on Redline, no `pkg/` move, no credential handling in DevX. |
| Mobile terminal view | Menu-only in v1: "Provider usage" entry in the existing ☰ menu. No header chip. |
| Thresholds | Match Redline's web dashboard: colors only — `<15%` remaining → red, `<35%` → amber, else normal. |

## What Redline exposes (verified against the running service)

- **Service:** `http://127.0.0.1:7436`, loopback-only by default (`internal/api/server.go:514-529 allowedHost`). Rejects requests carrying a foreign `Origin` header (`server.go:302-305`) → the SPA cannot call it directly; DevX's Go server must proxy.
- **Auth:** `Authorization: Bearer <token>`. Token file is `api-token` beside `redline.yaml`; standard macOS location `~/Library/Application Support/Redline/api-token` (`internal/cli/cli.go:493`). `REDLINE_API_TOKEN` env override (`cli.go:500-508`). Linux/other: `redline.yaml` in cwd or wherever the service was pointed — DevX config key `usage.redline.token_file` covers that.
- **`GET /v1/dashboard`** (`internal/api/dashboard.go:159-166`) — DB-only read, no provider network calls, cheap to poll. Relevant subset:

```json
{
  "generated_at": "...",
  "providers": [
    {
      "id": "claude-main", "provider": "claude", "paused": false,
      "snapshot": {
        "provider": "claude", "observed_at": "2026-09-04T16:31:23Z",
        "short":  { "remaining": 0.56, "resets_at": "2026-09-04T20:50:00Z" },
        "weekly": { "remaining": 0.15, "resets_at": "2026-09-04T16:59:59Z" },
        "allowances": [
          { "key": "session", "source_label": "Session", "scope": "account", "role": "short",  "remaining": 0.56, "resets_at": "...", "period_duration_seconds": 18000 },
          { "key": "weekly",  "source_label": "Weekly",  "scope": "account", "role": "weekly", "remaining": 0.15, "resets_at": "...", "period_duration_seconds": 604800 },
          { "key": "model:fable:weekly", "source_label": "Fable", "scope": "model", "role": "weekly", "remaining": 0.45, "resets_at": "...", "period_duration_seconds": 604800 }
        ],
        "source": "openusage", "confidence": "high"
      },
      "snapshot_stale": false,
      "error": "",
      "usage_source": { "active": "openusage", "consecutive_failures": 0, "last_error": "" }
    },
    {
      "id": "codex-main", "provider": "codex",
      "snapshot": {
        "weekly": { "remaining": 0, "resets_at": "2026-09-07T03:03:02Z" },
        "allowances": [
          { "key": "model:spark:short",  "source_label": "Spark",        "scope": "model", "role": "short",  "remaining": 0.55, "period_duration_seconds": 18000 },
          { "key": "model:spark:weekly", "source_label": "Spark Weekly", "scope": "model", "role": "weekly", "..." : "..." },
          { "key": "weekly", "scope": "account", "role": "weekly", "remaining": 0, "..." : "..." }
        ]
      }
    }
  ]
}
```

  Note the live Codex account: **no account-scoped 5h window** (`short` absent), weekly at 0%, but a model-scoped "Spark" 5h pool at 55%. The primary-window rule below has to handle this.

- **`POST /v1/providers/{id}/refresh`** (`server.go:626-633`) — forces a live fetch from the provider; `{id}` is the account id (`claude-main`), not the kind. 10s upstream timeout.
- **`GET /v1/dashboard/events`** — SSE, pushes the full dashboard every 5s. Not used in v1 (polling `/v1/dashboard` every 30s is simpler and Redline's own monitor only refreshes every 5m anyway); could replace polling later.
- **`snapshot_stale`** is computed server-side (`dashboard.go:277-290`, `> max_snapshot_age`, default 15m). DevX trusts it rather than re-deriving.
- **Redline's compact card** (`internal/api/dashboard/dashboard.js:118-146`) is the visual reference: `N% weekly` headline, `5h 62% · resets in 2h` / `No 5h limit`, progress bar; expanded = one `meter()` per window incl. `scope === 'model'`. Colors from `dashboard.js:29-31`: `<15` danger, `<35` warn.

## DevX-owned wire shape

`GET /api/usage` returns a DevX shape so Redline's model doesn't leak into the SPA:

```json
{
  "state": "ok",                             // ok | unavailable | disabled
  "message": "",                             // human text for unavailable/disabled
  "updated_at": "2026-09-04T17:02:11Z",      // when DevX last read Redline successfully
  "providers": [
    {
      "id": "claude-main",
      "provider": "claude",
      "label": "Claude",
      "state": "ok",                         // ok | stale | error
      "error": "",
      "source": "openusage",
      "observed_at": "2026-09-04T16:31:23Z",
      "primary": { "key": "session", "label": "5h", "remaining": 0.56, "resets_at": "...", "period_seconds": 18000 },
      "windows": [
        { "key": "session",            "label": "5-hour window", "scope": "account", "remaining": 0.56, "resets_at": "...", "period_seconds": 18000 },
        { "key": "weekly",             "label": "Weekly",        "scope": "account", "remaining": 0.15, "resets_at": "...", "period_seconds": 604800 },
        { "key": "model:fable:weekly", "label": "Fable",         "scope": "model",   "remaining": 0.45, "resets_at": "...", "period_seconds": 604800, "reset_inferred": false }
      ]
    }
  ]
}
```

**Primary window rule** (server-side, in Go, unit-tested): among `scope == "account"` windows pick the one with the smallest `period_seconds`; if there is no account-scoped short window, use account weekly and set `label: "wk"`. Model-scoped pools are never the primary — they're the detail view's job. For the live Codex account this yields `codex wk 0% · 2d`, which is the honest headline (Redline shows the same: "No 5h limit" + "0% weekly").

`windows[]` order: account short, account weekly, then model-scoped by role (short before weekly), then by label. Labels come from Redline's `source_label` with the account ones renamed to "5-hour window" / "Weekly" to match Redline's expanded view.

Never forwarded: tokens, raw Redline payload, tasks/runs/policy fields, `usage_source.last_error` (goes into `error` only when `snapshot` is missing).

## Refresh model

- **Poller** owned by `web.Server`: goroutine started in `Start()` (`web/server.go:56`) and `PrivateServer.Serve()` (`web/private.go:80`), stopped in `Shutdown`. Every `usage.poll_interval` (default **30s**; Redline's read is DB-only so this is cheap) it calls `GET /v1/dashboard`, maps to the wire shape, stores in memory, and broadcasts SSE `usage` via `s.hub.broadcastEvent("usage", payload)` (`web/sse.go`) **only when the payload changed** (compare marshalled bytes) so idle tabs don't churn.
- `GET /api/usage` returns the cached value; never blocks on Redline.
- `POST /api/usage/refresh` → `POST /v1/providers/{id}/refresh` for each provider (in parallel, 12s timeout) then an immediate dashboard re-read + broadcast. Throttle: one in-flight, 15s minimum spacing; extra requests return `202` with the cached state. Goes through the cookie-auth origin check like other state-changing endpoints.
- Redline unreachable / 401 / bad JSON → `state: "unavailable"` with a short message (`"Redline is not running on this host"`, `"Redline API token not found at …"`, `"Redline rejected the API token"`). Keep the last good `providers` for up to 15m so a Redline restart doesn't blank the strip; after that, clear them. Log the transition once, not every poll.
- Redline's own freshness (`snapshot_stale`) maps to per-provider `state: "stale"`.

SPA: `GET /api/usage` on mount, then SSE; `visibilitychange` → refetch (same guard as `SessionList.svelte:134-157`) for suspended mobile tabs.

## Config

Defaults in `cmd/root.go:96-125`, typed mirror + `SaveConfig` in `config/config.go`:

```yaml
usage:
  enabled: true                  # default true; state:"disabled" and no poller when false
  poll_interval: 30s
  redline:
    url: http://127.0.0.1:7436   # loopback only; refuse non-loopback unless `allow_remote: true`
    token_file: ""               # default: $REDLINE_API_TOKEN, else ~/Library/Application Support/Redline/api-token (darwin), else ~/.config/redline/api-token
```

`enabled: true` by default is fine because the failure mode with no Redline is one dim line, not an error. `GET /api/settings` (`web/api.go:196-205`) gains `"usage_enabled"` so the SPA skips mounting the widget when off.

## UI

### Compact strip (web, desktop, mobile sessions view)

New full-width row in `SessionList.svelte` directly above the key-hint footer (`SessionList.svelte:797`):

```
┌────────────────────────────────────────────┐
│ usage  claude 5h ▰▰▰▰▰▱▱▱▱▱ 56% · 4h    ▸   │
│        codex  wk ▰▱▱▱▱▱▱▱▱▱  0% · 2d        │
└────────────────────────────────────────────┘
```

- One line per provider: name · `primary.label` · progress bar · `NN%` · relative reset. Mono `text-[10px]` desktop, `text-[11px]` + `min-h-11` rows on mobile (matches `view` bar sizing at `SessionList.svelte:475-490`).
- Colors (Redline web thresholds): `<15%` `text-red-400`/`bg-red-500`, `<35%` `text-amber-300`/`bg-amber-400`, else `text-gray-300`/`bg-cyan-500`.
- Stale: gray-600 numbers + suffix `· stale 22m`. Provider error: one red line `claude: <short error>`.
- `state: "unavailable"`: single dim line `usage · redline not running` (desktop only; hidden on mobile to save space). `disabled`: not mounted. Loading: `usage · …`.
- The strip is one `<button aria-label="Provider usage details">` → opens the modal. Hotkey `u` (add to `handleKeydown` and the key-hint bar).

Why not the `h-10` header: it already has the wordmark and `[+ new]`; two providers don't fit legibly at `lg:w-72`, and a min-of-both number hides which provider is short.

### Detail modal (all surfaces)

`UsageDetailModal.svelte`, shell copied from `NewSessionModal.svelte:108-125` (bottom sheet on mobile, centered `max-w-sm` on `sm:`+, focus trap, `Esc`):

```
┌ provider usage ───────────────────── [↻] [×] ┐
│ CLAUDE                    openusage · sampled 4m ago
│ 5-hour window  ▰▰▰▰▰▱▱▱▱▱ 56% left · resets 20:50 (4h)
│ Weekly         ▰▱▱▱▱▱▱▱▱▱ 15% left · resets 17:00 (28m)
│ Fable          ▰▰▰▰▱▱▱▱▱▱ 45% left · resets 17:00 (28m)
│
│ CODEX                     native · sampled 4m ago
│ Weekly         ▱▱▱▱▱▱▱▱▱▱  0% left · resets Mon 03:03 (2d)
│ Spark          ▰▰▰▰▰▱▱▱▱▱ 55% left · resets 19:36 (3h)
│ Spark Weekly   ▰▰▰▰▰▰▱▱▱▱ 60% left · resets Mon 03:03 (2d)
│
│ via redline · polled 30s ago · ↻ refreshes providers now
│ [open redline dashboard]              (desktop/web only)
└───────────────────────────────────────────────┘
```

- One section per provider, one meter per `windows[]` entry, same colors. "Last known" prefix + sample age when stale. `reset_inferred` → `· reset inferred` suffix (as Redline does).
- `[↻]` → `POST /api/usage/refresh`; spinner until the next SSE `usage` event or 15s.
- `unavailable` → body is the message plus a hint: "Start Redline (`redline serve`) to see provider usage."
- "open redline dashboard" link → `http://127.0.0.1:7436/` via `openExternal()` on desktop, plain `target=_blank` on web. Hidden on mobile (loopback isn't reachable from a phone).
- Mounted once in `App.svelte` (like `AskApprovalModal`), opened by a `devx:showUsage` `CustomEvent` so the strip, the mobile menu, the hotkey, and the desktop menu share one path.

### Mobile terminal view — menu only

`MobileActionsMenu.svelte`: new section header `usage` + item **"Provider usage"** → `window.dispatchEvent(new CustomEvent('devx:showUsage'))`. Hidden when `usage_enabled` is false.

### Desktop (Wails)

`desktop/main.go:80-110`: `devxMenu.AddText("Provider Usage…", keys.CmdOrCtrl("u"), …)` dispatching `devx:showUsage`. Nothing else — desktop hosts the same `web/dist` and the poller runs inside `PrivateServer`. Update `desktop/main_test.go` if it enumerates event names.

### TUI

Deferred. `usage/` is a standalone package so the TUI can later show one footer line using the same client.

## Files

```
usage/                            new package (importable by web/ and later tui/)
  client.go                       RedlineClient{BaseURL, Token, HTTPClient}: Dashboard(ctx), Refresh(ctx, id); token discovery (env → token_file → platform default)
  model.go                        wire types; MapDashboard(redlineJSON) → Usage; primaryWindow(); label normalization; window ordering
  poller.go                       Poller{Client, Interval, KeepStaleFor, Now, OnChange}; Start/Stop; Current(); Refresh() with throttle
  client_test.go, model_test.go, poller_test.go   httptest fake Redline (fixtures from the live payload above incl. the Codex no-short case); 401/unreachable/stale/throttle
web/
  usage.go                        handleUsage, handleUsageRefresh; wiring to hub.broadcastEvent("usage")
  usage_test.go
  server.go, private.go           start/stop poller
  api.go                          settings: usage_enabled
  sse.go                          (no change; new event name only)
cmd/root.go, config/config.go, config/config_test.go   usage.* keys

web/app/src/
  api.js                          getUsage(), refreshUsage(), subscribeToEvents({…, usage})
  lib/usage/usageFormat.js        tone(remaining) → 'ok'|'warn'|'danger' (15/35); percent(); relativeReset(); absoluteReset()
  lib/usage/usageFormat.test.js   vitest
  lib/usage/UsageStrip.svelte
  lib/usage/UsageDetailModal.svelte
  lib/SessionList.svelte          mount strip; `u` hotkey; key-hint entry
  lib/terminal/MobileActionsMenu.svelte   "Provider usage" item
  App.svelte                      usage state; fetch on mount; SSE; visibilitychange; devx:showUsage listener; modal mount
web/app/tests/usage.spec.js       Playwright at 1280px and 390px: strip (ok/warn/danger/stale/unavailable), click → modal, Esc, refresh POST, mobile ☰ → modal
web/app/tests/session-list.spec.js  add /api/usage + usage_enabled mocks
web/dist                          rebuild + commit
desktop/main.go                   menu item
```

## Implementation order

1. `usage/` package with fixtures + tests (pure Go, no server). Includes the primary-window rule and mapping.
2. Backend wiring: config keys, poller lifecycle in `Start`/`Serve`/`Shutdown`, `GET /api/usage`, `POST /api/usage/refresh`, SSE, settings flag. Handler tests.
3. SPA: `usageFormat.js` + tests, strip, modal, `App.svelte` state/SSE, `SessionList` mount + hotkey, Playwright spec, mocks in existing spec, rebuild `web/dist`.
4. Mobile menu item + desktop menu item.
5. Manual verification against the live Redline on this Mac; screenshots as DevX artifacts.

## UX States

| State | Strip | Modal |
|---|---|---|
| ok | `claude 5h ▰▰▰▰▰▱▱▱▱▱ 56% · 4h` colored by threshold | full meters |
| no account short window | `codex wk ▰▱▱▱▱▱▱▱▱▱ 0% · 2d` | weekly + model meters |
| stale | same numbers, gray, `· stale 22m` | "Last known" prefix, sample age |
| provider error | one red line | full error + `[↻]` |
| redline unavailable | `usage · redline not running` (desktop only) | message + `redline serve` hint |
| disabled | not mounted | menu entry hidden |
| loading | `usage · …` | skeleton meters |

## Verification

- `go test -race ./usage/... ./web/... ./config/... ./cmd/...`
- `cd web/app && npm test && npm run test:ui`
- Manual: `devx web` with Redline running → both providers match `redline status --provider claude-main` / Redline dashboard; stop Redline → strip degrades to "not running" and recovers on restart; `[↻]` triggers a fresh `observed_at`.
- Desktop: `Cmd+U` opens modal; "open redline dashboard" opens the browser.
- Mobile (390px viewport in Playwright + a real phone via the tunnel): strip visible in sessions view; ☰ → Provider usage opens the modal in terminal view.
- Proof artifacts: desktop + mobile screenshots via `devx artifact add`.

## Acceptance Criteria

- Web, desktop, and mobile sessions view show remaining % and reset for each provider's primary window with no click.
- One action (strip click, `u`, mobile ☰ → Provider usage, desktop `Cmd+U`) opens the detail view with all windows incl. model-scoped.
- DevX never reads provider credentials; only the Redline API token, which never crosses `/api/*`.
- Redline down/absent produces one dim line, not errors, and recovers automatically.
- Colors match Redline's web dashboard (`<15` red, `<35` amber).
- Existing Playwright suite passes with the new mocks.

## Resolved Questions

1. **Non-macOS token default** — `~/.config/redline/api-token`, with `usage.redline.token_file` overriding and `REDLINE_API_TOKEN` taking precedence over both (`usage/client.go` `DiscoverToken`).
2. **`[↻]` scope** — one global button that refreshes all providers in parallel (capped at 4 concurrent).
3. **Poll interval** — kept at 30s polling; Redline's SSE stays deferred.

## Deviations from the plan as written

Recorded during implementation; none change the delivered UX.

- **`Provider.Error` is suppressed unless the provider has no snapshot.** The plan forwarded Redline's per-account error text alongside a usable snapshot. Security review flagged that this text can contain filesystem paths, so it is now dropped for the ok/stale cases and capped at 200 runes otherwise (`usage/model.go`).
- **Redline connection settings are read from the global config only.** The plan implied ordinary viper resolution. Because a project-level `.devx/config.yaml` is repo-supplied, it could have pointed the client at a remote host and named an arbitrary 0600 file as the "token", exfiltrating it. `usage.redline.{url,token_file,allow_remote}` now come from `~/.config/devx/config.yaml` or `DEVX_*` env only (`web.UsageOptionsFromGlobalConfig`). `usage.enabled` and `usage.poll_interval` still honor project overrides.
- **A bad Redline URL degrades instead of failing startup.** The plan made a non-loopback URL a startup error. It still returns an error from `ConfigureUsage`, but `devx web` and the desktop shell log it and run with usage disabled, matching the feature's "one dim line, not an error" failure model.
- **`poll_interval` has a 5s floor** (`usage.MinPollInterval`). A bare number such as `poll_interval: 30` parses as 30ns and would have busy-looped against Redline.
- **`POST /api/usage/refresh` always returns the `Usage` shape**, including for 404/502, so the SPA has one parser rather than branching on status before decoding.
- **Desktop uses plain `Cmd+U`.** `Cmd+Shift+U` was already View Terminal Output; the two are distinct accelerators, so neither is ambiguous.
- **A `loading` state was added** to the wire shape. The plan's UX table distinguished loading from unavailable, but the original state enum could not express it, leaving the SPA to sniff message strings.

## Deferred

- Redline SSE instead of polling.
- TUI footer line; `devx usage` CLI.
- Threshold notifications (`notifications.js` + SSE make this cheap).
- Mobile header chip (revisit after v1 on a phone).
- Fallback to importing Redline as a library when no service is running (would require Redline `pkg/` move + credential handling in DevX; v1 explicitly avoids this).
