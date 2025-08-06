# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

`devx` is a macOS CLI tool for managing local development environments with Git worktrees, automatic port allocation, and tmux session management. It's written in Go and uses Cobra for the CLI framework.

## Common Development Commands

### Building
```bash
make build      # Build binary with version info
make dev        # Quick development build (no version info)
make install    # Install to $GOPATH/bin
```

### Testing
```bash
make test       # Run all tests
go test ./...   # Alternative: run all tests
go test ./cmd   # Run tests for specific package
```

### Code Quality
```bash
go mod tidy                      # Clean up dependencies
gofmt -w .                       # Format all Go files
go vet ./...                     # Run Go vet for static analysis
go test -race ./...              # Run tests with race detector
```

## Architecture & Code Organization

### Core Components

1. **CLI Commands** (`cmd/`): All CLI commands using Cobra framework
   - `root.go`: Base command setup and TUI launcher
   - `session*.go`: Session management commands (create, list, attach, rm, flag)
   - `config*.go`: Configuration management
   - `caddy.go`: Caddy health check and route management
   - `project*.go`: Project registry management

2. **Session Management** (`session/`): Core business logic for development sessions
   - `metadata.go`: Session persistence and state management
   - `worktree.go`: Git worktree operations
   - `ports.go`: Dynamic port allocation
   - `tmuxp.go`: tmux session configuration generation
   - `editor.go`: Editor integration (Cursor, VS Code, etc.)
   - `envrc.go`: Environment variable file generation
   - `bootstrap.go`: Bootstrap file copying for new sessions
   - `cleanup.go`: Session cleanup operations

3. **Configuration** (`config/`): Configuration and settings management
   - `config.go`: Core configuration logic using Viper
   - `discovery.go`: Project-level config discovery (walks up directory tree)
   - `projects.go`: Project registry for quick access

4. **Caddy Integration** (`caddy/`): HTTPS proxy management
   - `routes.go`: Route creation and management via Caddy API
   - `health.go`: Caddy health checks and auto-repair
   - `provisioning.go`: Caddy configuration and startup helpers

5. **TUI** (`tui/`): Terminal User Interface using Bubble Tea
   - `model.go`: TUI state and model
   - `run.go`: TUI execution logic
   - `styles.go`: Visual styling

### Key Design Patterns

- **Session Metadata**: All session state is persisted in `sessions.json` files (global or project-level)
- **Configuration Hierarchy**: Project configs (`.devx/`) override global configs (`~/.config/devx/`)
- **Port Allocation**: Uses `go-getport` for finding available ports dynamically
- **Service Abstraction**: Services are defined by name (ui, api, etc.) with automatic port/host generation
- **Template System**: Uses Go templates for tmuxp configuration generation
- **Error Handling**: Consistent error wrapping with context throughout

### Important Implementation Details

- **Git Worktree Management**: Creates isolated development environments at `.worktrees/<session-name>`
- **Environment Variables**: Generated `.envrc` files contain `<SERVICE>_PORT` and `<SERVICE>_HOST` variables
- **Caddy Routes**: Creates HTTPS routes like `https://<session>-<service>.localhost`
- **Attention Flags**: Visual notification system for sessions needing attention
- **Editor Tracking**: Monitors editor PIDs to detect when editors are closed
- **Bootstrap Files**: Copies specified files from project root to new worktrees
- **Cleanup Commands**: Executes custom cleanup scripts when removing sessions

### Testing Strategy

- Unit tests are colocated with source files (`*_test.go`)
- Integration tests use `_integration_test.go` suffix
- Tests use standard Go testing package
- tmux integration tests require tmux to be installed

## Dependencies

The project uses Go modules. Key dependencies:
- `spf13/cobra`: CLI framework
- `spf13/viper`: Configuration management
- `charmbracelet/bubbletea`: TUI framework
- `go-resty/resty`: HTTP client for Caddy API
- `jsumners/go-getport`: Port allocation

## Workflow Tips

- When modifying CLI commands, update the help text and examples
- Session metadata changes should maintain backward compatibility
- Caddy route operations should handle API failures gracefully
- TUI changes should preserve keyboard navigation patterns
- Configuration changes should respect the hierarchy (env vars > project > global)