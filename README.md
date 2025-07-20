# devx

A macOS CLI tool for managing local development environments with Git worktrees, automatic port allocation, and tmux session management.

## Features

- **Interactive TUI**: Beautiful terminal interface for browsing and managing sessions
- **Live Session Preview**: Real-time view of tmux session content in the TUI
- **Attention Flags**: Visual notifications when sessions need attention (perfect for Claude Code integration)
- **Git Worktree Management**: Create isolated development environments with separate branches
- **Dynamic Port Allocation**: Automatically allocate unique ports for your services
- **Environment Configuration**: Generate `.envrc` files with port assignments and session variables
- **tmux Integration**: Launch preconfigured tmux sessions with multiple windows
- **Editor Integration**: Automatically launch your preferred editor (Cursor, VS Code, etc.) with session folders
- **Session Management**: List, attach to, and remove development sessions with full cleanup
- **Configurable Templates**: Customize tmux layouts and port names without recompiling
- **Session Persistence**: Track and manage multiple development sessions with metadata
- **Caddy HTTP Integration**: Automatic HTTP proxy routes for .localhost domains
- **Dependency Checking**: Automatic validation of required tools with installation guidance

## Dependencies

### Required
- **Git**: For worktree management
- **Go 1.22+**: For building the tool

### Optional but Recommended
- **tmux**: For session management (ships with macOS)
- **tmuxp**: For tmux session configuration (`pip install tmuxp`)
- **direnv**: For automatic environment variable loading
- **Caddy**: For HTTPS proxy functionality (`brew install caddy`)

## Installation

```bash
git clone https://github.com/jfox85/devx
cd devx
make build
# Move to your PATH, e.g.:
mv devx /usr/local/bin/
```

**Alternative installation methods:**
```bash
# Development build (no version info)
make dev

# Install directly to GOPATH/bin
make install

# Build manually without Makefile
go build -o devx main.go
```

## Quick Start

1. **Initialize configuration**:
   ```bash
   devx config init
   ```

2. **Navigate to your Git repository and create a session**:
   ```bash
   cd /path/to/your/project
   devx session create feature-branch
   ```

3. **This will**:
   - Create a Git worktree at `.worktrees/feature-branch`
   - Allocate unique ports for your services
   - Generate `.envrc` with environment variables
   - Generate `.tmuxp.yaml` with session configuration
   - Launch a tmux session (if tmux/tmuxp available)
   - Create HTTPS routes via Caddy (if enabled and available)

## Configuration

### Project-Level Configuration

devx supports project-level configuration that takes precedence over global settings. This is perfect for team projects or repositories with specific requirements.

**Create a project-level config:**
```bash
# In your project root
mkdir .devx
cp ~/.config/devx/config.yaml .devx/config.yaml
# Edit .devx/config.yaml as needed
```

**Project-level files:**
- `.devx/config.yaml` - Project-specific configuration
- `.devx/sessions.json` - Project-specific sessions (isolated from global)
- `.devx/session.yaml.tmpl` - Project-specific tmux template

**Configuration Discovery:**
devx walks up the directory tree looking for a `.devx` directory, starting from your current working directory. If found, it uses project-level configs first, then falls back to global configs.

**Benefits:**
- Team projects can share consistent devx configuration
- Different projects can have different port configurations
- Project sessions are isolated from global sessions
- Custom tmux templates per project

### Global Configuration

Global configuration is stored in `~/.config/devx/config.yaml`:

```yaml
basedomain: localhost
caddy_api: http://localhost:2019
tmuxp_template: ~/.config/devx/session.yaml.tmpl
disable_caddy: false
editor: ""  # Uses VISUAL or EDITOR env vars if empty
ports:
  - ui
  - api
bootstrap_files:  # Files to copy from project root to each new worktree
  - .env.example
  - scripts/setup.sh
  - config/local.json
cleanup_command: ""  # Command to run when removing sessions (optional)
```

### View Configuration
```bash
devx config view
```

### Update Configuration
```bash
# Set individual values
devx config set basedomain "dev.local"
devx config set caddy_api "http://localhost:2020"

# Configure services
devx config set ports "ui,api,database,cache"
# or with JSON array
devx config set ports '["ui", "api", "database"]'

# Disable Caddy integration
devx config set disable_caddy true

# Configure preferred editor
devx config set editor "cursor"
devx config set editor "code"  # VS Code
devx config set editor "nvim"  # Neovim

# Configure bootstrap files (copy from project root to each new worktree)
devx config set bootstrap_files ".env.example,scripts/setup.sh,config/local.json"
# or with JSON array
devx config set bootstrap_files '["docker-compose.local.yml", "scripts/init.sh"]'

# Configure cleanup command (runs when removing sessions)
devx config set cleanup_command "scripts/teardown.sh"
devx config set cleanup_command "docker-compose -f docker-compose.local.yml down"
```

### Environment Variable Overrides

Any configuration value can be overridden with environment variables using the `DEVX_` prefix:

```bash
DEVX_BASEDOMAIN=custom.local devx config view
```

## Usage

### Terminal User Interface (TUI)

Launch the interactive TUI to browse and manage sessions:

```bash
devx
```

**TUI Features:**
- **Session Browser**: Navigate sessions with arrow keys or vim bindings (j/k)
- **Live Preview**: View tmux session content in real-time
- **Attention Flags**: Visual indicators (🔔) for sessions needing attention
- **Quick Actions**: 
  - `Enter`: Attach to selected session
  - `c`: Create new session
  - `r`: Remove selected session
  - `?`: Toggle help
  - `q`: Quit

**Attention Flags in TUI:**
- Flagged sessions appear with a bell icon (🔔) and are sorted to the top
- Preview pane shows attention banner with reason and timestamp
- Flags are automatically cleared when you attach to a session

### Session Management

#### Create a Session
```bash
# Create with default/configured ports
devx session create my-feature

# Create with custom ports (legacy)
devx session create my-feature --fe-port 8080 --api-port 8081

# Create without launching tmux
devx session create my-feature --no-tmux

# Handle existing worktree conflicts
devx session create my-feature --detach
```

#### List Sessions
```bash
# View all active sessions with status
devx session list

# Example output:
# NAME               BRANCH             PORTS                    HOSTS                               STATUS
# feature-auth       feature-auth       WEB:3000,API:3001       ui.localhost,api.localhost         tmux:attached,editor:running
# hotfix-bug         hotfix-bug         WEB:3002,API:3003       ui.localhost,api.localhost         tmux:detached,editor:stopped
```

#### Attach to Session
```bash
# Attach to existing session (launches tmux and editor)
devx session attach my-feature

# This will:
# - Attach to the tmux session if it exists
# - Launch editor if configured and not running
# - Relaunch editor if it was closed
```

#### Remove Session
```bash
# Clean up session completely
devx session rm my-feature

# This removes:
# - tmux session
# - Editor processes
# - Caddy HTTPS routes  
# - Git worktree
# - Session metadata
```

#### Session Attention Flags

Mark sessions for attention (perfect for Claude Code integration):

```bash
# Flag a session with a reason
devx session flag my-feature "Ready for review"

# Flag without a reason
devx session flag my-feature

# Unflag a session
devx session flag my-feature --clear

# Force flag even if it's the current session
devx session flag my-feature "Force flag" --force
```

**Attention Flag Features:**
- Visual indicators in TUI and session list
- Automatic clearing when attaching to session
- Cannot flag the currently active session (use --force to override)
- Timestamps track when flags were set
- Flags are sorted to the top in TUI

#### Bootstrap Files

Automatically copy files from your project root to each new worktree. Perfect for config files, setup scripts, and environment templates that you need but don't want to commit to the repository.

**Configure bootstrap files:**
```bash
# Add files to copy
devx config set bootstrap_files ".env.example,scripts/setup.sh,docker-compose.local.yml"

# View current bootstrap files
devx config get bootstrap_files
```

**Example bootstrap_files configuration:**
```yaml
bootstrap_files:
  - .env.example          # Environment template
  - scripts/setup.sh      # Setup script
  - scripts/init.sh       # Initialization script
  - config/local.json     # Local configuration
  - docker-compose.local.yml  # Local Docker setup
  - .vscode/settings.json # Editor settings (if not in repo)
```

**Bootstrap file behavior:**
- Files are copied from project root to the new worktree
- Preserves file permissions and directory structure
- Warns about missing files but continues session creation
- Paths must be relative to project root
- Security: Prevents directory traversal (e.g., `../../../etc/passwd`)

**Common use cases:**
- Environment variable templates (`.env.example` → `.env`)
- Local development configuration files
- Setup and initialization scripts
- IDE/editor configuration
- Local Docker compose overrides
- Test data or fixtures

### Cleanup Command

Automatically run cleanup commands when removing sessions. Perfect for tearing down Docker containers, databases, external services, or any infrastructure that needs cleanup.

**Configure cleanup command:**
```bash
# Set cleanup command
devx config set cleanup_command "scripts/teardown.sh"

# View current cleanup command
devx config get cleanup_command
```

**Example cleanup commands:**
```bash
# Docker cleanup
devx config set cleanup_command "docker-compose -f docker-compose.local.yml down"

# Custom script
devx config set cleanup_command "scripts/cleanup.sh"

# Multiple commands (using shell)
devx config set cleanup_command "npm run cleanup && docker system prune -f"

# Conditional cleanup
devx config set cleanup_command "[ -f docker-compose.local.yml ] && docker-compose -f docker-compose.local.yml down || true"
```

**Environment variables available to cleanup command:**
- `SESSION_NAME` - The session name (e.g., `my-feature`)
- `WORKTREE_PATH` - Full path to the worktree directory
- `SESSION_BRANCH` - The git branch name
- `UI_PORT`, `API_PORT`, etc. - All configured service ports
- `UI_HOST`, `API_HOST`, etc. - All configured service hostnames (if Caddy is enabled)

**Example cleanup script:**
```bash
#!/bin/bash
# scripts/teardown.sh

echo "Cleaning up session: $SESSION_NAME"

# Stop any running containers for this session
docker stop "${SESSION_NAME}-db" 2>/dev/null || true
docker rm "${SESSION_NAME}-db" 2>/dev/null || true

# Clean up any test databases
psql -c "DROP DATABASE IF EXISTS test_${SESSION_NAME}" 2>/dev/null || true

# Remove any temporary files
rm -rf "/tmp/${SESSION_NAME}-*"

# Log cleanup
echo "$(date): Cleaned up session $SESSION_NAME" >> ~/.devx-cleanup.log

echo "Cleanup completed for session: $SESSION_NAME"
```

**Cleanup command behavior:**
- Runs from the worktree directory (`cd` to session path)
- Has access to all session environment variables
- Executed through shell (`sh -c`) for complex commands
- 30-second timeout to prevent hanging
- Warnings shown for failures but don't stop session removal
- Runs after Caddy routes are removed but before git worktree removal

**Common use cases:**
- Stop and remove Docker containers
- Drop test databases
- Clean up temporary files
- Remove external service resources
- Notify external systems of teardown
- Log cleanup activities

### Session Structure
When you create a session named `my-feature`, devx creates:

```
.worktrees/my-feature/
├── .envrc              # Environment variables
├── .tmuxp.yaml         # tmux session configuration
├── .git               # Git worktree metadata
├── .env.example        # Bootstrap file (if configured)
├── scripts/            # Bootstrap directory (if configured)
│   └── setup.sh        # Bootstrap file (if configured)
└── ... (your project files)
```

### Port Configuration

#### Default Services
By default, devx allocates two services:
- `ui`: Frontend/web server
- `api`: Backend API server

#### Custom Service Names
Configure any number of services with meaningful names:

```bash
# For a microservices setup
devx config set ports "ui,api,auth,database,redis,queue"

# For a full-stack app with services
devx config set ports "frontend,backend,database,cache"
```

#### Generated Environment Variables
The `.envrc` file will contain:
```bash
export UI_PORT=52815
export API_PORT=52816
export DATABASE_PORT=52817
export CACHE_PORT=52818

# HTTPS hostnames (when Caddy is enabled)
export UI_HOST=https://my-feature-ui.localhost
export API_HOST=https://my-feature-api.localhost
export DATABASE_HOST=https://my-feature-database.localhost
export CACHE_HOST=https://my-feature-cache.localhost

export SESSION_NAME=my-feature
```

### tmux Integration

#### Default tmux Layout
Each session creates a tmux session with three windows:
- **editor**: For code editing
- **backend**: For running backend services
- **frontend**: For running frontend development server

#### Custom tmux Templates
Customize your tmux layout by editing `~/.config/devx/session.yaml.tmpl`:

```yaml
session_name: {{.Name}}
start_directory: {{.Path}}
windows:
  - window_name: editor
    layout: tiled
    shell_command_before:
      - cd {{.Path}}
    panes:
      - echo "Editor window - Session: {{.Name}}"
      - nvim  # or your preferred editor

  - window_name: backend
    layout: tiled
    shell_command_before:
      - cd {{.Path}}{{range $name, $port := .Ports}}
      - export {{$name}}={{$port}}{{end}}
      - export SESSION_NAME={{.Name}}
    panes:
      - echo "Backend running on port $API_PORT"
      - npm run dev  # or your backend start command

  - window_name: frontend
    layout: tiled
    shell_command_before:
      - cd {{.Path}}{{range $name, $port := .Ports}}
      - export {{$name}}={{$port}}{{end}}
      - export SESSION_NAME={{.Name}}
    panes:
      - echo "Frontend running on port $WEB_PORT"
      - npm run start  # or your frontend start command
```

#### Manual tmux Operations
```bash
# List tmux sessions
tmux list-sessions

# Attach to existing session
tmux attach -t my-feature

# Load session manually
tmuxp load .worktrees/my-feature/.tmuxp.yaml
```

### direnv Integration

If you have [direnv](https://direnv.net/) installed, devx automatically runs `direnv allow` so environment variables are loaded when you `cd` into the worktree directory.

```bash
cd .worktrees/my-feature
echo $API_PORT  # Shows allocated port number
echo $SESSION_NAME  # Shows "my-feature"
```

### Caddy HTTPS Integration

devx can automatically create HTTPS routes through [Caddy](https://caddyserver.com/) for your development services.

#### Hostname Environment Variables

When Caddy routes are created, devx automatically adds hostname environment variables to your `.envrc` file:

```bash
# For services: ui=3000, api=3001, database=5432
export UI_PORT=3000
export API_PORT=3001
export DATABASE_PORT=5432

# HTTPS hostnames
export UI_HOST=https://my-session-ui.localhost
export API_HOST=https://my-session-api.localhost
export DATABASE_HOST=https://my-session-database.localhost

export SESSION_NAME=my-session
```

**Use in your applications:**
```bash
# Shell scripts
curl $API_HOST/health

# Node.js
fetch(process.env.UI_HOST + '/api/users')

# Python
import os
requests.get(f"{os.environ['API_HOST']}/data")
```

#### Setup Caddy

```bash
# Install Caddy
brew install caddy

# Option 1: Use the provided helper scripts
~/.config/devx/caddy-start.sh  # Start Caddy in background
~/.config/devx/caddy-stop.sh   # Stop Caddy

# Option 2: Start Caddy manually with admin API
caddy run --config /dev/null --adapter caddyfile

# Option 3: Use the devx Caddyfile
caddy run --config ~/.config/devx/Caddyfile
```

**Verify Caddy is running:**
```bash
curl http://localhost:2019/config/
```

#### Automatic Route Creation

When you create a session, devx automatically:
- Creates HTTPS routes for each service port
- Maps ports to logical service names
- Provides secure .localhost domain access

```bash
# Create session (with Caddy running)
devx session create my-feature

# This creates routes like:
# https://my-feature-ui.localhost -> 127.0.0.1:3000
# https://my-feature-api.localhost -> 127.0.0.1:3001
# https://my-feature-db.localhost -> 127.0.0.1:5432
```

#### Service Configuration

Configure service names directly - devx will create matching port and host environment variables:

**Configuration:**
```bash
devx config set ports "ui,api,database,cache"
```

**Generated Environment Variables:**
- Service `ui` → `UI_PORT` and `UI_HOST`
- Service `api` → `API_PORT` and `API_HOST` 
- Service `database` → `DATABASE_PORT` and `DATABASE_HOST`
- Service `cache` → `CACHE_PORT` and `CACHE_HOST`

**DNS Normalization:**
Service names are automatically converted to be DNS-compatible:
- Uppercase → lowercase
- Underscores → hyphens
- Invalid characters removed

**Examples:**
- `UI` → `ui` → Route: `https://session-ui.localhost`
- `API_SERVICE` → `api-service` → Route: `https://session-api-service.localhost`
- `STREAMING_IMAGES` → `streaming-images` → Route: `https://session-streaming-images.localhost`

#### Configuration

```bash
# Configure Caddy API endpoint
devx config set caddy_api "http://localhost:2019"

# Disable Caddy integration entirely
devx config set disable_caddy true

# Change base domain (advanced)
devx config set basedomain "dev.local"
```

#### Manual Route Management

```bash
# Session cleanup automatically removes routes
devx session rm my-feature

# Check Caddy routes manually
curl http://localhost:2019/config/
```

## Examples

### Basic Web Development Workflow

```bash
# Configure for web development
devx config set ports "ui,api"

# Create session
devx session create user-auth-feature

# Your tmux session launches with:
# - Window 1: Editor ready
# - Window 2: Backend with API_PORT=3001 and API_HOST=https://user-auth-feature-api.localhost exported
# - Window 3: Frontend with UI_PORT=3000 and UI_HOST=https://user-auth-feature-ui.localhost exported
# - HTTPS routes: https://user-auth-feature-ui.localhost, https://user-auth-feature-api.localhost

# In backend window: npm run dev
# In frontend window: npm start
# Access via: https://user-auth-feature-ui.localhost or $UI_HOST
```

### Microservices Development

```bash
# Configure for microservices
devx config set ports "gateway,auth,user,order,payment,database"

# Create session
devx session create payment-service

# All services get unique ports and HTTPS routes with hostname env vars:
# GATEWAY_PORT=5000 -> GATEWAY_HOST=https://payment-service-gateway.localhost
# AUTH_PORT=5001 -> AUTH_HOST=https://payment-service-auth.localhost  
# USER_PORT=5002 -> USER_HOST=https://payment-service-user.localhost
# ORDER_PORT=5003 -> ORDER_HOST=https://payment-service-order.localhost
# PAYMENT_PORT=5004 -> PAYMENT_HOST=https://payment-service-payment.localhost
# DATABASE_PORT=5005 -> DATABASE_HOST=https://payment-service-database.localhost

# Use in your code:
# curl $AUTH_HOST/validate
# fetch(process.env.API_HOST + '/users')
```

### Multiple Parallel Features

```bash
# Work on multiple features simultaneously
devx session create feature-a    # Gets ports 3000, 3001
devx session create feature-b    # Gets ports 3002, 3003  
devx session create hotfix-123   # Gets ports 3004, 3005

# Each in separate worktrees with isolated environments
ls .worktrees/
# feature-a/  feature-b/  hotfix-123/
```

## Advanced Configuration

### Custom Template Variables

The tmux template receives these variables:
- `{{.Name}}`: Session name
- `{{.Path}}`: Worktree path
- `{{.Ports}}`: Map of port name to port number

You can iterate over ports:
```yaml
{{range $name, $port := .Ports}}
- export {{$name}}={{$port}}{{end}}
```

### Configuration File Locations

**Project-level (takes precedence):**
- `.devx/config.yaml` - Project configuration
- `.devx/sessions.json` - Project sessions (isolated)
- `.devx/session.yaml.tmpl` - Project tmux template

**Global (fallback):**
- `~/.config/devx/config.yaml` - Global configuration
- `~/.config/devx/session.yaml.tmpl` - Global tmux template
- `~/.config/devx/sessions.json` - Global sessions

**Configuration Discovery:**
devx searches for a `.devx` directory starting from your current working directory and walking up the directory tree. If found, project-level configs take precedence over global configs.

### Dependency Checking

Check system dependencies and get installation guidance:

```bash
# Check all dependencies
devx check

# Example output:
# Dependency Check (devx v1.0.0):
# =================
# ✓ Git (git version 2.39.0) - Version control system for managing worktrees
# ✓ Tmux (tmux 3.3a) - Terminal multiplexer for session management
# ✗ Tmuxp - Tmux session manager
#   └─ Install with: pip install tmuxp
# ✓ Caddy (v2.6.4) - Web server for local development routing
# ✓ Direnv (2.32.2) - Environment variable management (recommended)
# ✓ Editor (cursor 0.29.3) - Configured editor: cursor
#
# ℹ️ Missing optional dependencies: Tmuxp
#    These are recommended but not required.
```

**Automatic Dependency Warnings:**
- Quiet dependency check runs when launching TUI
- Shows warnings for missing required dependencies
- Notes for missing optional dependencies
- Run `devx check` for detailed installation instructions

### Version Information

Display version and build information:

```bash
# Show version
devx version
# or
devx --version
devx -v

# Detailed version info
devx version --detailed

# JSON output
devx version --output json
```

### Editor Integration

devx can automatically launch your preferred editor when creating or attaching to sessions.

#### Editor Configuration Priority
1. `devx config set editor "command"` - devx-specific setting
2. `VISUAL` environment variable
3. `EDITOR` environment variable

#### Editor Commands
```bash
# Configure specific editors
devx config set editor "cursor"     # Cursor
devx config set editor "code"       # VS Code
devx config set editor "nvim"       # Neovim
devx config set editor "subl"       # Sublime Text

# Or use environment variables
export VISUAL="cursor"
export EDITOR="nvim"
```

#### Editor Behavior
- **Session Create**: Automatically launches editor with the new worktree path
- **Session Attach**: Launches editor if not running, or reuses existing instance
- **Session Remove**: Terminates tracked editor processes
- **PID Tracking**: Monitors editor processes to detect when they close

## Troubleshooting

### Dependency Issues
```bash
# Check all dependencies with installation hints
devx check

# Common issues:
# - Missing tmuxp: pip install tmuxp
# - Missing caddy: brew install caddy  
# - Missing direnv: brew install direnv
# - Editor not found: Check devx config get editor
```

### tmux Issues
If tmux doesn't launch automatically:
```bash
# Check if tmux is available
which tmux

# Check if tmuxp is available  
which tmuxp

# Install tmuxp
pip install tmuxp

# Launch manually
tmuxp load .worktrees/my-feature/.tmuxp.yaml
```

### Port Conflicts
If you get port allocation errors:
```bash
# Check what's using ports
lsof -i :3000

# Kill conflicting processes
kill -9 <PID>
```

### direnv Not Working
```bash
# Install direnv
brew install direnv

# Add to your shell (add to ~/.zshrc or ~/.bashrc)
eval "$(direnv hook zsh)"  # for zsh
eval "$(direnv hook bash)" # for bash

# Allow the directory
cd .worktrees/my-feature
direnv allow
```

### Git Worktree Issues
```bash
# List all worktrees
git worktree list

# Remove problematic worktree
git worktree remove .worktrees/feature-name

# Prune stale worktree references
git worktree prune
```

### Editor Issues
```bash
# Check if editor command is available
devx config get editor
which cursor  # or your configured editor

# Test editor configuration
devx config set editor "cursor"
devx session create test-editor

# Clear editor PID if stuck
devx session rm session-name
```

### Session Management Issues
```bash
# List all sessions with status
devx session list

# Force remove stuck session
devx session rm session-name

# Check session metadata
cat ~/.config/devx/sessions.json

# Clean up orphaned tmux sessions
tmux list-sessions
tmux kill-session -t session-name

# Clear stuck attention flags
devx session flag session-name --clear

# Check current session detection
devx session flag current-session  # Should show warning
```

### Caddy Issues

If HTTPS routes aren't working:
```bash
# Check if Caddy is running
curl http://localhost:2019/config/

# Start Caddy manually
caddy run --config /dev/null --adapter caddyfile

# Disable Caddy integration if not needed
devx config set disable_caddy true

# Check route creation manually
curl -X GET http://localhost:2019/config/apps/http/servers/srv0/routes
```

If you get certificate warnings:
- .localhost domains use self-signed certificates
- Add security exception in your browser, or
- Install Caddy's root certificate for trusted HTTPS

## Contributing

1. Fork the repository
2. Create a feature branch: `devx session create my-new-feature`
3. Make your changes
4. Test thoroughly
5. Submit a pull request

## License

MIT License - see LICENSE file for details.