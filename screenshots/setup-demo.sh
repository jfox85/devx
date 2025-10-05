#!/bin/bash
# Setup complete demo environment with fake tmux sessions

set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
DEVX_CONFIG="$HOME/.config/devx"
DEMO_DIR="$SCRIPT_DIR/demo-env/.config/devx"

# Check if Caddy is running with srv1 server
if ! curl -s http://localhost:2019/config/apps/http/servers/srv1/routes > /dev/null 2>&1; then
    echo "⚠️  Caddy is not running with srv1 server"
    echo "Start with: caddy start --config $SCRIPT_DIR/caddy-config.json"
    exit 1
fi
echo "✓ Caddy is running"

mkdir -p "$DEVX_CONFIG"

# Backup and replace sessions
if [ ! -f "$DEVX_CONFIG/sessions.json.backup" ] && [ -f "$DEVX_CONFIG/sessions.json" ]; then
    cp "$DEVX_CONFIG/sessions.json" "$DEVX_CONFIG/sessions.json.backup"
fi
cp "$DEMO_DIR/sessions.json" "$DEVX_CONFIG/sessions.json"
echo "✓ Demo sessions installed"

# Backup and replace projects
if [ ! -f "$DEVX_CONFIG/projects.json.backup" ] && [ -f "$DEVX_CONFIG/projects.json" ]; then
    cp "$DEVX_CONFIG/projects.json" "$DEVX_CONFIG/projects.json.backup"
fi
cp "$DEMO_DIR/projects.json" "$DEVX_CONFIG/projects.json"
echo "✓ Demo projects installed"

# Create fake tmux sessions with demo content
chmod +x "$SCRIPT_DIR/create-demo-tmux-sessions.sh"
"$SCRIPT_DIR/create-demo-tmux-sessions.sh"

# Create Caddy routes for demo sessions
chmod +x "$SCRIPT_DIR/create-demo-caddy-routes.sh"
"$SCRIPT_DIR/create-demo-caddy-routes.sh"

echo ""
echo "✓ Demo environment ready for recording"
echo "Run restore-demo.sh when done."
