#!/bin/bash
# Create Caddy routes for demo sessions

set -e

CADDY_API="http://localhost:2019"

echo "Creating Caddy routes for demo sessions..."

# Helper function to create a route
create_route() {
    local route_id=$1
    local hostname=$2
    local port=$3

    curl -sS --fail -X POST "$CADDY_API/config/apps/http/servers/srv1/routes" \
        -H "Content-Type: application/json" \
        -d "{
            \"@id\": \"$route_id\",
            \"match\": [{
                \"host\": [\"$hostname\"]
            }],
            \"handle\": [{
                \"handler\": \"reverse_proxy\",
                \"upstreams\": [{
                    \"dial\": \"localhost:$port\"
                }]
            }]
        }" > /dev/null

    echo "✓ $hostname → localhost:$port"
}

# feature-auth routes (webapp project)
create_route "sess-webapp-feature-auth-ui" "webapp-feature-auth-ui.localhost" 3000
create_route "sess-webapp-feature-auth-api" "webapp-feature-auth-api.localhost" 4000

# bugfix-login routes (webapp project)
create_route "sess-webapp-bugfix-login-ui" "webapp-bugfix-login-ui.localhost" 3001
create_route "sess-webapp-bugfix-login-api" "webapp-bugfix-login-api.localhost" 4001

# refactor-api routes (backend project)
create_route "sess-backend-refactor-api-api" "backend-refactor-api-api.localhost" 5000
create_route "sess-backend-refactor-api-db" "backend-refactor-api-db.localhost" 5432

# docs-update routes (website project)
create_route "sess-website-docs-update-ui" "website-docs-update-ui.localhost" 8080

# experiment-cache routes (backend project)
create_route "sess-backend-experiment-cache-api" "backend-experiment-cache-api.localhost" 5001
create_route "sess-backend-experiment-cache-cache" "backend-experiment-cache-cache.localhost" 6379

echo ""
echo "✓ Created 9 Caddy routes"
