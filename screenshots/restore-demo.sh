#!/bin/bash
# Restore original data and clean up demo tmux sessions

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
DEVX_CONFIG="$HOME/.config/devx"

# Kill demo tmux sessions
chmod +x "$SCRIPT_DIR/kill-demo-tmux-sessions.sh"
"$SCRIPT_DIR/kill-demo-tmux-sessions.sh"

# Delete demo Caddy routes
chmod +x "$SCRIPT_DIR/delete-demo-caddy-routes.sh"
"$SCRIPT_DIR/delete-demo-caddy-routes.sh"

# Restore sessions
if [ -f "$DEVX_CONFIG/sessions.json.backup" ]; then
    mv "$DEVX_CONFIG/sessions.json.backup" "$DEVX_CONFIG/sessions.json"
    echo "✓ Original sessions restored"
else
    rm -f "$DEVX_CONFIG/sessions.json"
    echo "✓ Removed demo sessions (no backup found)"
fi

# Restore projects
if [ -f "$DEVX_CONFIG/projects.json.backup" ]; then
    mv "$DEVX_CONFIG/projects.json.backup" "$DEVX_CONFIG/projects.json"
    echo "✓ Original projects restored"
else
    rm -f "$DEVX_CONFIG/projects.json"
    echo "✓ Removed demo projects (no backup found)"
fi

echo ""
echo "✓ Demo environment cleaned up"
