#!/bin/bash
# Kill all demo tmux sessions

SESSIONS=(
  "feature-auth"
  "bugfix-login"
  "refactor-api"
  "docs-update"
  "experiment-cache"
)

echo "Killing demo tmux sessions..."

for session in "${SESSIONS[@]}"; do
  if tmux has-session -t "$session" 2>/dev/null; then
    tmux kill-session -t "$session"
    echo "✓ Killed $session"
  fi
done

echo "✓ Demo tmux sessions cleaned up"
