# DevX Context

## Glossary

### Target
The execution environment where a session runs. Values: `host` (default — bare metal, no isolation), `docker` (containerized), future: `gatepost` (Docker + egress-control proxy), `vm` (microVM or full VM), `remote` (remote server instance). Selected via `--target <type>` flag or `target:` config key. The same session configuration (worktree, ports, tmuxp template, services) works regardless of target type.

### Session
A named development environment consisting of a git worktree, allocated ports, tmux layout, Caddy/Cloudflare routes, and optional container isolation. Sessions are the primary unit of work in DevX.

### Worktree
A git worktree checked out for a session. The persistent filesystem that survives container restarts. For Docker targets, the worktree is bind-mounted into the container at `/workspace`.
