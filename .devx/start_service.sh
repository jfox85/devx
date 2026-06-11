#!/bin/bash
# .devx/start_service.sh - Start a devx web dev service (dogfooding the new UI).
#
# Services:
#   web  - the Go backend (devx web), foreground so it does NOT touch the global
#          ~/.config/devx/web.pid daemon file. Bound to 0.0.0.0 via DEVX_WEB_BIND
#          so Docker port publishing reaches it when the session runs in a
#          container. Port comes from the devx-assigned $WEB env var.
#   ui   - the vite dev server, proxying /api and /terminal to the backend.

set -e

SERVICE_NAME="$1"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="$PROJECT_DIR/.devx/logs"
PID_DIR="$PROJECT_DIR/.devx/pids"

mkdir -p "$LOG_DIR" "$PID_DIR"

# Read the dev token. Prefer the bootstrapped copy in the worktree, fall back to
# the main checkout. The file is gitignored and never committed.
read_token() {
    local f
    for f in "$PROJECT_DIR/.devx-web-token" "$HOME/projects/devx/.devx-web-token"; do
        if [[ -f "$f" ]]; then
            tr -d '[:space:]' < "$f"
            return 0
        fi
    done
    echo "ERROR: dev token not found. Create ~/projects/devx/.devx-web-token with a fixed token." >&2
    return 1
}

start_service() {
    case "$SERVICE_NAME" in
        web)
            cd "$PROJECT_DIR"
            local token
            token="$(read_token)"
            echo "Starting devx web backend on 0.0.0.0:$WEB"
            DEVX_WEB_PORT="$WEB" \
            DEVX_WEB_BIND="0.0.0.0" \
            DEVX_WEB_SECRET_TOKEN="$token" \
                go run . web > "$LOG_DIR/$SERVICE_NAME.log" 2>&1 &
            echo $! > "$PID_DIR/$SERVICE_NAME.pid"
            ;;
        ui)
            cd "$PROJECT_DIR/web/app"
            if [[ ! -d node_modules ]]; then
                echo "Installing web/app dependencies..."
                npm install
            fi
            echo "Starting vite dev server on 0.0.0.0:$UI (backend http://localhost:$WEB)"
            DEVX_WEB_ORIGIN="http://localhost:$WEB" \
                npm run dev -- --host 0.0.0.0 --port "$UI" > "$LOG_DIR/$SERVICE_NAME.log" 2>&1 &
            echo $! > "$PID_DIR/$SERVICE_NAME.pid"
            ;;
        *)
            echo "Unknown service: $SERVICE_NAME" >&2
            exit 1
            ;;
    esac
}

echo "Starting $SERVICE_NAME service..."
start_service
SERVICE_PID="$(cat "$PID_DIR/$SERVICE_NAME.pid")"
echo "$SERVICE_NAME started (PID: $SERVICE_PID)"
echo "Logs: $LOG_DIR/$SERVICE_NAME.log"

# Keep the script in the foreground so tmux/devx sees the service as running.
wait "$SERVICE_PID"
