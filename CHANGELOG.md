# Changelog

All notable changes to devx are recorded here. Versions follow
[Semantic Versioning](https://semver.org/) (pre-1.0: minor bumps may carry
breaking changes, which are always called out).

## [v0.4.0] - 2026-10-22

Changes since v0.3.0 (2026-03-10).

**No breaking changes since v0.3.0.** Every v0.3.0 CLI command and flag, web
API route, config key and `sessions.json` field is still present. Session
identity colors (`devx session color`, `--color`) were added (#36) and then
removed (#68) during this cycle, so they never shipped in a tagged release.
Existing `sessions.json` files load unchanged.

### Pi / MCP agents
- Managed Pi agents expose mid-task progress events and `latest_progress` (#100).
- **Managed Pi agents over a local stdio MCP server.** New `devx mcp pi` and
  `devx agent` commands (`list`, `status`, `inspect`, `attach`, `adopt`,
  `relaunch`, `takeover`, `release`) let you start Pi agents, message them,
  check their results and cancel tasks. A human can take over an agent and
  hand it back. Managed agents run in local-only sessions, which have no
  routes, tunnels or published ports (#71).
- **Artifact bridge for agents:** `devx_artifact_list` and
  `devx_artifact_read` reach only files registered in a session's manifest.
  Uploads are off by default and need an explicit session list. The policy is
  reloaded live and fails closed when invalid. Sessions get stable instance
  ids (#72).
- **Queue inspection and bounded cancel:** each task reports its delivery
  phase (queued, delivered, accepted, finished). Queued prompts can be edited
  with compare-and-swap. Cancel outcomes are always recorded (#82).
- `pi_list_sessions` is paginated, with a cursor, totals and a response-size
  bound (#99).
- Bridge durability: an unsaved human takeover fails closed, terminal
  receipts are lossless, and a receipt hit by a permanent errno is deferred
  instead of quarantined (#88, #92).
- `devx session markers` restores lost local-only ownership markers from
  reviewed backups (dry run by default, journaled, can be rolled back).
  Session files keep JSON fields this build doesn't know (#83).
- `devx session instances --rollback` accepts only authenticated run
  directories and plan-matched journals, and keeps restored agents
  restorable (#86, #91).
- **Cross-session ask approvals:** `devx ask` (`list`, `pending`, `read`,
  `approve`, `deny`), with an optional agent responder and
  `devx session context` output for agents (#55).
- **Security hardening:** the managed-agent launch policy (`pi_mcp.*`,
  `pi_command`, `pi_args`, `pass_env`) and the `agent_responder.*` policy are
  read only from the owner's global config. A repository's
  `.devx/config.yaml` or `DEVX_*` env vars can no longer widen them
  (#87, #90).

### Artifacts
- **Session artifacts:** `devx artifact add|list|url|archive|remove` and an
  HTTP API cover file and text artifacts, tags, retention and focus. Also
  adds a web artifact pane, search overlay, badges, notifications, a
  share-target flow and PWA support (#38).
- Artifact folders (`--folder`), a tree view and folder-aware search (#40).
- Unseen-artifact tracking (#46). `devx show <path>` pushes an image to the
  web UI (#36).
- A fullscreen artifact viewer with keyboard and swipe navigation, open and
  download actions, and native Save dialogs on desktop (#97).
- Hardened JSX previews (stricter CSP; plain-text source in desktop mode),
  and image/artifact path inserts now go to the composer (#54, #62).

### Web and desktop
- A **DevX desktop app** (Wails/macOS) with a control deck, a terminal iframe
  keep-alive pool, a quick switcher, native shortcuts and notifications (#45).
  It also adds image paste and drop (#49, #50), better terminal font and focus
  (#51), clickable terminal links (#53), and a bundled CLI so a stale `devx`
  on PATH can't wipe pinned sessions (#65).
- An operator-console UI refresh, a Claude/Codex usage widget fed by a local
  Redline instance (`usage.*` config), and composer drafts that persist (#63).
- Recent and Pinned session views in the web UI and the TUI (#58). Also
  reworks session row layouts (#59, #60, #61).
- A pane viewer and mobile terminal scrollback (#37), Option-drag selection
  in ttyd (#39), the Hack Nerd Font for the terminal (#35), and the web UI as
  a containerized devx service with a configurable bind address (#41).

### CLI and sessions
- `devx session recover` restores missing sessions and agents after a crash or
  reboot, with a dry run for reviewing the recovery plan (#104).
- **Session targets:** Docker and Gatepost targets alongside host, plus
  `devx session exec` and `devx session gatepost` (#44, #48).
- Stale-session cleanup: `devx session stale`, `devx session prune` and
  `devx session review`, with status indicators (#46, #47).
- Session display names and `devx session rename` (#36).
- Per-project default session targets (#52, #53). `bootstrap_files` are now
  resolved from the project config (#42).
- Builds stamp the version only from `v*` release tags. A new
  `make install-local` target does reproducible local installs (#96, #98).

### Fixes
- Config initialization now creates both the config and tmuxp template, preserves
  existing files, supports explicit destinations, and permits retries after a
  failed write. Dependency checks distinguish core worktrees from optional
  terminal/proxy tools, with actionable setup guidance (#106).
- The quick switcher and composer no longer steal focus or drop Enter presses;
  pending session selection remains consistent (#105).
- Security and compatibility dependency updates include the Go 1.26.9 release
  toolchain and patched Go/web dependencies (#101, #102).
- The terminal WebSocket no longer returns 403 behind Caddy or a Cloudflare
  tunnel (#43).
- Desktop terminal typing no longer stops after about 30 characters (#66).
- Multi-line pastes stay one bracketed paste (#67).
- Tab focus stays inside modal overlays (#70).
- Usage refresh is no longer wrongly throttled (#69).
- A template's real tmux window 0 is no longer killed on restore (#93).
- Windows portability: home-directory lookup, permission checks and
  lock-free session reads during atomic rename (#73, #76, #77).
- The desktop app build works when run from any directory (#64).

### Build, CI and repo
- The public installer handles long release metadata without broken-pipe
  failures and links to the installed version's documentation. Built
  artifacts and release preparation run an onboarding smoke check (#106).
- The release workflow installs tmux, so GoReleaser's `go test ./...`
  pre-release hook passes on the release runner.
- Windows tests are now a real CI gate (#73). The macOS CI tmuxp is pinned
  to Python 3.14 (#94).
- Coverage improvements (#57, #95), repo cleanup (#56), and Claude Code
  GitHub workflows (#85).

## [v0.3.0] - 2026-03-10

- Web interface with a browser terminal and mobile support (#33, #34).

[v0.4.0]: https://github.com/jfox85/devx/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/jfox85/devx/releases/tag/v0.3.0
