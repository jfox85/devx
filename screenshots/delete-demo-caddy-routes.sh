#!/bin/bash
# Delete Caddy routes for demo sessions

CADDY_API="http://localhost:2019"

echo "Deleting demo Caddy routes..."

# List of route IDs to delete
ROUTE_IDS=(
  "sess-webapp-feature-auth-ui"
  "sess-webapp-feature-auth-api"
  "sess-webapp-bugfix-login-ui"
  "sess-webapp-bugfix-login-api"
  "sess-backend-refactor-api-api"
  "sess-backend-refactor-api-db"
  "sess-website-docs-update-ui"
  "sess-backend-experiment-cache-api"
  "sess-backend-experiment-cache-cache"
)

for route_id in "${ROUTE_IDS[@]}"; do
  curl -s -X DELETE "$CADDY_API/id/$route_id" > /dev/null 2>&1 || true
done

echo "✓ Deleted demo Caddy routes"
