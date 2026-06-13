#!/bin/bash
# .devx/start_service.sh - Run the devx web UI as a containerized devx service.
#
# This runs INSIDE the session container. The single "web" service builds the
# Svelte SPA into web/dist (which the Go server embeds via //go:embed) and then
# runs `devx web` bound to 0.0.0.0:$WEB so Docker port publishing + Caddy + the
# CF tunnel can reach it like any normal service.
#
# To see UI changes: edit web/app, then restart this service (it rebuilds dist
# and re-runs the Go server, which re-embeds the fresh dist). There is no vite
# hot reload in this mode by design (option B: single built service).

set -e

SERVICE_NAME="$1"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="$PROJECT_DIR/.devx/logs"

mkdir -p "$LOG_DIR"

# Read the fixed dev token from the worktree. devx bootstraps it here from the
# project root via bootstrap_files. Gitignored; never committed.
read_token() {
    local f="$PROJECT_DIR/.devx-web-token"
    if [[ -f "$f" ]]; then
        tr -d '[:space:]' < "$f"
        return 0
    fi
    echo "ERROR: dev token not found at $f. Create .devx-web-token in the devx project root." >&2
    return 1
}

start_service() {
    case "$SERVICE_NAME" in
        web)
            if [[ -z "${WEB:-}" ]]; then
                echo "ERROR: WEB port not set by devx" >&2
                exit 1
            fi
            local token
            token="$(read_token)"

            # Build the SPA so the Go server embeds the current frontend.
            # Install deps when missing or when package-lock has changed since
            # the last install (so a dep bump doesn't build against stale deps).
            cd "$PROJECT_DIR/web/app"
            if [[ ! -d node_modules ]] || [[ package-lock.json -nt node_modules ]]; then
                echo "Installing web/app dependencies..."
                npm ci
            fi
            echo "Building web UI (web/dist)..."
            npm run build

            # exec the Go server bound to 0.0.0.0 so the published port + Caddy
            # can reach it. exec replaces this script as the pane's foreground
            # process: signals and the real exit code propagate, and tmux/devx
            # reflect the true service state. Logs are tee'd so they appear in
            # the pane and in the log file. This is a foreground server, so it
            # does NOT touch the global web.pid daemon used by the host devx web.
            cd "$PROJECT_DIR"
            echo "Starting devx web backend on 0.0.0.0:$WEB (logs: $LOG_DIR/$SERVICE_NAME.log)"
            exec env \
                DEVX_WEB_PORT="$WEB" \
                DEVX_WEB_BIND="0.0.0.0" \
                DEVX_WEB_TRUSTED_PROXIES="$(ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | paste -sd, -)" \
                DEVX_WEB_SECRET_TOKEN="$token" \
                go run . web > >(tee "$LOG_DIR/$SERVICE_NAME.log") 2>&1
            ;;
        *)
            echo "Unknown service: $SERVICE_NAME" >&2
            exit 1
            ;;
    esac
}

echo "Starting $SERVICE_NAME service..."
start_service
