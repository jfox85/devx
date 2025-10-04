# Generating DevX Demo Screenshots

This guide explains how to regenerate the demo screenshot and provides context for future modifications.

## Quick Start

```bash
cd screenshots

# 1. Start Caddy (if not already running)
caddy start --config caddy-config.json

# 2. Set up demo environment
./setup-demo.sh

# 3. Generate the screenshot
vhs tui-final.tape

# 4. Clean up
./restore-demo.sh

# Optional: Stop Caddy
caddy stop
```

## How It Works

### The Problem
We needed professional TUI screenshots without exposing real session/project data, but the TUI preview panel requires actual running tmux sessions to display content.

### The Solution
1. **Fake Sessions** - Create realistic session metadata with demo names
2. **Real Tmux Sessions** - Create actual tmux sessions with scripted terminal output
3. **Caddy Routes** - Create matching routes so health checks pass
4. **VHS Recording** - Record the TUI with VHS, hiding the initial command

### Architecture

```
setup-demo.sh
├── Backup real sessions.json & projects.json
├── Install demo sessions.json & projects.json (5 sessions, 3 projects)
├── Create 5 tmux sessions with scripted output
│   ├── feature-auth     → React dev server
│   ├── bugfix-login     → Test runner
│   ├── refactor-api     → GraphQL server
│   ├── docs-update      → Next.js build
│   └── experiment-cache → Redis performance
└── Create 9 Caddy routes matching sessions

vhs tui-final.tape
└── Records TUI interaction (hidden startup, navigation, preview, hostnames, create)

restore-demo.sh
├── Kill 5 demo tmux sessions
├── Delete 9 Caddy routes
└── Restore original sessions.json & projects.json
```

## Demo Sessions

The demo includes 5 realistic sessions across 3 projects:

**E-Commerce Web App (webapp)**
- `feature-auth` - OAuth integration feature branch
- `bugfix-login` - Login validation bug fix (has attention flag)

**Backend Services (backend)**
- `refactor-api` - REST to GraphQL migration
- `experiment-cache` - Redis caching experiment

**Marketing Website (website)**
- `docs-update` - API v2 documentation updates

## Files

### Generated Assets
- **`tui-final.gif`** - The main demo screenshot (1.2MB)

### Generation Scripts
- **`setup-demo.sh`** - Sets up complete demo environment
- **`restore-demo.sh`** - Cleans up demo environment
- **`create-demo-tmux-sessions.sh`** - Creates 5 fake tmux sessions with scripted output
- **`kill-demo-tmux-sessions.sh`** - Kills demo tmux sessions
- **`create-demo-caddy-routes.sh`** - Creates 9 Caddy routes for demo sessions
- **`delete-demo-caddy-routes.sh`** - Removes demo Caddy routes
- **`tui-final.tape`** - VHS recording script

### Configuration
- **`caddy-config.json`** - Caddy server configuration with srv1 server
- **`demo-env/.config/devx/sessions.json`** - 5 fake demo sessions
- **`demo-env/.config/devx/projects.json`** - 3 fake demo projects

## For Future AI Agents

### Context
This screenshot generation system was created to solve a specific problem: We needed to showcase devx's TUI for the README/marketing, but:
- Real session names exposed private project information
- TUI preview panel shows tmux session content, which would leak code/data
- Simply removing routes caused Caddy health warnings
- "Session not running" previews looked unprofessional

### Key Design Decisions

1. **Why real tmux sessions?**
   - The TUI preview uses `tmux capture-pane` to get actual session content
   - Mocking this would require modifying devx code
   - Real sessions with scripted output is simpler and cleaner

2. **Why Caddy routes?**
   - devx health check expects routes for each port in sessions.json
   - Missing routes trigger warnings in the TUI
   - Creating routes makes the demo look production-ready

3. **Why VHS instead of screencapture?**
   - VHS creates scriptable, reproducible recordings
   - Can be regenerated when devx features change
   - Better quality than macOS screencapture
   - Animations are more engaging than static images

### Modifying the Demo

**To change session content:**
Edit `create-demo-tmux-sessions.sh` and update the heredoc content for each session.

**To add/remove sessions:**
1. Update `demo-env/.config/devx/sessions.json`
2. Update `demo-env/.config/devx/projects.json` if needed
3. Add corresponding tmux session in `create-demo-tmux-sessions.sh`
4. Add corresponding routes in `create-demo-caddy-routes.sh`
5. Update cleanup scripts accordingly

**To change TUI interactions:**
Edit `tui-final.tape` - it's a simple script with Type, Sleep, Enter, Down, Up, Escape commands.

**To change visual styling:**
Edit the VHS settings at the top of `tui-final.tape` (FontSize, Width, Height, Theme).

### Troubleshooting

**Caddy warnings still appear:**
- Ensure routes match the session names and project aliases exactly
- Route ID format: `sess-{project}-{session}-{service}`
- Hostname format: `{project}-{session}-{service}.localhost`

**Tmux sessions show "Session not running":**
- Verify tmux sessions exist: `tmux ls`
- Check session names match sessions.json exactly
- Ensure tmux sessions have content (not blank)

**VHS recording shows command prompt:**
- Make sure `Hide`/`Show` are in the right places in the .tape file
- Hide should be before the devx command, Show after TUI loads

## Dependencies

- **tmux** - For creating demo sessions
- **caddy** - For route management
- **vhs** - For recording terminal sessions (`brew install vhs`)
- **jq** - For JSON manipulation (optional, for debugging)

## Notes

- The demo sessions use ports that might conflict with real services (3000, 4000, etc.)
- Caddy must be running with the `srv1` server (not `srv0`)
- Generated GIF is ~1.2MB - suitable for GitHub READMEs
- All demo data is intentionally fake and generic
