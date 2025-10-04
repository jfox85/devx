#!/bin/bash
# Create demo tmux sessions with fake but realistic terminal output

set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# Session names from demo sessions.json
SESSIONS=(
  "feature-auth"
  "bugfix-login"
  "refactor-api"
  "docs-update"
  "experiment-cache"
)

echo "Creating demo tmux sessions..."

# Kill any existing demo sessions first
for session in "${SESSIONS[@]}"; do
  tmux kill-session -t "$session" 2>/dev/null || true
done

# Session 1: feature-auth - React dev server with OAuth
tmux new-session -d -s "feature-auth"
tmux send-keys -t "feature-auth" "clear" C-m
sleep 0.5
tmux send-keys -t "feature-auth" "cat << 'EOF'" C-m
tmux send-keys -t "feature-auth" "

  vite v4.5.0 dev server running at:

  > Local:   http://localhost:3000/
  > Network: http://192.168.1.100:3000/

  ready in 421ms.

  10:34:12 AM [vite] page reload src/components/OAuthButton.tsx
  10:34:18 AM [vite] hmr update /src/hooks/useAuth.ts
  10:35:02 AM [vite] hmr update /src/pages/Login.tsx

  ✓ OAuth integration tests passing (12/12)
  → Implementing token refresh flow...

EOF" C-m
echo "✓ feature-auth"

# Session 2: bugfix-login - Test runner
tmux new-session -d -s "bugfix-login"
tmux send-keys -t "bugfix-login" "clear" C-m
sleep 0.5
tmux send-keys -t "bugfix-login" "cat << 'EOF'" C-m
tmux send-keys -t "bugfix-login" "

  PASS  tests/auth/login.test.ts
    ✓ validates email format (8ms)
    ✓ validates password strength (12ms)
    ✓ shows error for invalid credentials (15ms)
    ✓ redirects after successful login (9ms)

  PASS  tests/auth/validation.test.ts
    ✓ allows valid email addresses (5ms)
    ✓ rejects malformed emails (7ms)
    ✓ enforces minimum password length (6ms)

  Test Suites: 2 passed, 2 total
  Tests:       7 passed, 7 total
  Snapshots:   0 total
  Time:        2.451s

  Watching for file changes...

EOF" C-m
echo "✓ bugfix-login"

# Session 3: refactor-api - GraphQL server
tmux new-session -d -s "refactor-api"
tmux send-keys -t "refactor-api" "clear" C-m
sleep 0.5
tmux send-keys -t "refactor-api" "cat << 'EOF'" C-m
tmux send-keys -t "refactor-api" "

  🚀 GraphQL Server ready at http://localhost:5000/graphql

  [2024-10-04 10:36:15] INFO  Database connected: postgres://localhost:5432/backend_dev
  [2024-10-04 10:36:16] INFO  Redis cache connected: redis://localhost:6379
  [2024-10-04 10:36:17] INFO  Schema compiled: 47 types, 23 queries, 15 mutations

  Query: users (12ms)
  Query: products (8ms) [cached]
  Mutation: createOrder (45ms)
  Query: orders (6ms)

  → Migrating REST endpoints to GraphQL...
  → /api/users     → Query.users ✓
  → /api/products  → Query.products ✓

EOF" C-m
echo "✓ refactor-api"

# Session 4: docs-update - Next.js docs site
tmux new-session -d -s "docs-update"
tmux send-keys -t "docs-update" "clear" C-m
sleep 0.5
tmux send-keys -t "docs-update" "cat << 'EOF'" C-m
tmux send-keys -t "docs-update" "

  ▲ Next.js 14.0.3
  - Local:        http://localhost:8080
  - Network:      http://0.0.0.0:8080

  ✓ Ready in 1.2s

  ○ Compiling /api-reference/v2 ...
  ✓ Compiled /api-reference/v2 in 234ms

  ○ Compiling /docs/authentication ...
  ✓ Compiled /docs/authentication in 189ms

  GET /api-reference/v2 200 in 245ms
  GET /_next/static/css/app.css 200 in 12ms

  → Updating API v2 documentation...

EOF" C-m
echo "✓ docs-update"

# Session 5: experiment-cache - Redis experiment
tmux new-session -d -s "experiment-cache"
tmux send-keys -t "experiment-cache" "clear" C-m
sleep 0.5
tmux send-keys -t "experiment-cache" "cat << 'EOF'" C-m
tmux send-keys -t "experiment-cache" "

  [Express] Server running on port 5001
  [Redis] Connected to redis://localhost:6379

  Cache Performance Test:
  ┌─────────────────┬──────────┬───────────┬──────────┐
  │ Operation       │ Without  │ With      │ Speedup  │
  ├─────────────────┼──────────┼───────────┼──────────┤
  │ User lookup     │ 45ms     │ 3ms       │ 15.0x    │
  │ Product search  │ 123ms    │ 8ms       │ 15.4x    │
  │ Order history   │ 234ms    │ 12ms      │ 19.5x    │
  └─────────────────┴──────────┴───────────┴──────────┘

  Cache hit rate: 87.3%
  → Redis cache integration working! ✓

EOF" C-m
echo "✓ experiment-cache"

echo ""
echo "✓ Created 5 demo tmux sessions"
echo "Verify with: tmux ls"
