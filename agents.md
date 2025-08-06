# AI Agent Integration with devx

This guide documents how AI coding assistants (Claude, ChatGPT, GitHub Copilot, etc.) can integrate with the devx development environment tool to create seamless AI-assisted development workflows.

## Overview

devx is designed with AI agent integration in mind, providing:

- **Attention Flag System**: Built-in signaling mechanism for AI workflow coordination
- **Isolated Development Environments**: Git worktree-based sessions for safe AI experimentation
- **Automatic Environment Setup**: Port allocation, environment variables, and service routing
- **Editor Integration**: Seamless launching of AI-compatible editors (Cursor, VS Code, etc.)
- **Session Management**: Persistent tracking of AI development sessions

## Quick Start

### 1. Basic Setup

```bash
# Initialize devx configuration
devx config init

# Set your preferred AI-compatible editor
devx config set editor cursor  # or "code" for VS Code

# Create a new AI development session
devx session create my-ai-project

# The session automatically includes a 'claude' pane in tmux
# and launches your configured editor
```

### 2. AI Workflow Integration

```bash
# Flag a session when AI work is complete
devx session flag my-ai-project claude_done

# Clear the flag when you've reviewed the changes
devx session flag my-ai-project --clear

# View all sessions with attention flags in the TUI
devx
```

## Core Features

### Attention Flag System

The attention flag system enables seamless coordination between AI agents and human developers:

#### Setting Attention Flags

```bash
# Flag with predefined reasons
devx session flag <session-name> claude_done
devx session flag <session-name> claude_stuck
devx session flag <session-name> manual

# Flag with custom reason
devx session flag <session-name> "custom reason"

# Force flag even if it's the current session
devx session flag <session-name> claude_done --force
```

#### Clearing Attention Flags

```bash
# Clear any attention flag
devx session flag <session-name> --clear
```

#### Visual Indicators

The devx TUI displays visual indicators for flagged sessions:
- **🔔 Attention Icon**: Sessions needing attention
- **Reason Display**: Shows why the session was flagged
- **Timestamp**: When the flag was set

### Session Management

#### Creating AI Development Sessions

```bash
# Create session with automatic port allocation
devx session create ai-feature-branch

# Create session for specific project
devx session create ai-refactor --project my-app

# Create session with custom ports
devx session create ai-service --fe-port 3000 --api-port 8080
```

#### Session Structure

Each devx session provides:

```
my-ai-project/
├── .envrc              # Environment variables with allocated ports
├── .tmuxp.yaml         # Tmux configuration with claude pane
└── <your-project-files>
```

#### Environment Variables

devx automatically generates environment variables for each session:

```bash
# Example .envrc content
export FE_PORT=3001
export API_PORT=8001
export DB_PORT=5433
export SESSION_NAME=my-ai-project
export FE_HOST=http://my-ai-project-fe.localhost
export API_HOST=http://my-ai-project-api.localhost
```

### Tmux Integration

#### Default Session Layout

The default tmux session includes:

1. **Editor Window**: 
   - Contains a `claude` pane for running AI commands
   - Automatically launches your configured editor

2. **Repo-root Window**:
   - Terminal in the project root
   - All environment variables loaded
   - Ready for development commands

#### Customizing the Claude Pane

The `claude` pane in the editor window is designed for running AI agent commands. You can:

```bash
# Run Claude Desktop or CLI tools
claude

# Run other AI assistants
chatgpt-cli
copilot

# Or use it for any AI-related tooling
```

### Editor Integration

#### Supported Editors

devx integrates with AI-compatible editors:

- **Cursor**: AI-first code editor
- **VS Code**: With AI extensions
- **Any editor**: Via configuration

#### Configuration

```bash
# Set your preferred editor
devx config set editor cursor
devx config set editor code
devx config set editor "cursor --wait"

# Verify editor configuration
devx config get editor
```

#### Editor Process Management

devx tracks editor processes:
- Automatically launches editor when creating sessions
- Tracks editor PID for session management
- Handles editor cleanup on session removal

## Workflow Patterns

### Pattern 1: AI Feature Development

```bash
# 1. Create isolated development environment
devx session create feature-ai-search --project main-app

# 2. Work with AI agent in the claude pane
# AI agent makes changes to the codebase

# 3. AI flags session when complete
devx session flag feature-ai-search claude_done

# 4. Review changes in TUI (shows attention flag)
devx

# 5. Test and validate changes
cd ~/.devx/sessions/feature-ai-search
npm test

# 6. Clear flag after review
devx session flag feature-ai-search --clear

# 7. Merge or continue development
git add . && git commit -m "AI-assisted feature implementation"
```

### Pattern 2: AI Code Review and Refactoring

```bash
# 1. Create session for refactoring task
devx session create refactor-auth --project backend

# 2. AI analyzes code and suggests improvements
# Work happens in the claude pane

# 3. Flag when AI analysis is complete
devx session flag refactor-auth claude_done

# 4. Human reviews AI suggestions
# Make additional changes if needed

# 5. Clear flag and proceed
devx session flag refactor-auth --clear
```

### Pattern 3: Multi-Agent Collaboration

```bash
# 1. Create session for complex task
devx session create complex-feature

# 2. First AI agent works on backend
devx session flag complex-feature claude_backend_done

# 3. Second AI agent works on frontend
devx session flag complex-feature gpt_frontend_done

# 4. Human coordinates and integrates
devx session flag complex-feature --clear
```

## Configuration

### Global Configuration

```bash
# View current configuration
devx config

# Set editor for AI development
devx config set editor cursor

# Configure base domain for local development
devx config set basedomain localhost

# Disable Caddy if not needed
devx config set disable_caddy true
```

### Session Templates

Customize the tmux session template at `~/.config/devx/session.yaml.tmpl`:

```yaml
session_name: {{.Name}}
start_directory: {{.Path}}
windows:
  - window_name: ai-workspace
    panes:
      - claude          # AI agent pane
      - cursor .        # Editor pane
  
  - window_name: development
    panes:
      - npm run dev     # Development server
      - npm run test    # Test runner
```

## Best Practices

### 1. Session Naming

Use descriptive names that indicate the AI task:

```bash
devx session create ai-refactor-user-auth
devx session create claude-implement-search
devx session create gpt-optimize-queries
```

### 2. Attention Flag Usage

- **Be Specific**: Use descriptive reasons for flags
- **Clear Promptly**: Clear flags after reviewing AI work
- **Use Force Sparingly**: Only force-flag when necessary

```bash
# Good
devx session flag my-session "claude completed user authentication"

# Better than generic
devx session flag my-session claude_done
```

### 3. Environment Isolation

- Use separate sessions for different AI tasks
- Keep experimental AI changes isolated
- Test AI-generated code thoroughly before merging

### 4. Port Management

Let devx handle port allocation automatically:

```bash
# Good - automatic allocation
devx session create ai-microservice

# Only specify ports when necessary
devx session create ai-integration --fe-port 3000 --api-port 8080
```

### 5. Editor Integration

Configure your editor for optimal AI development:

```bash
# For Cursor (AI-first editor)
devx config set editor cursor

# For VS Code with AI extensions
devx config set editor code

# With specific flags
devx config set editor "cursor --wait --new-window"
```

## Troubleshooting

### Common Issues

#### 1. Editor Not Launching

```bash
# Check editor configuration
devx config get editor

# Verify editor is available
which cursor  # or your configured editor

# Test editor manually
cursor /path/to/project
```

#### 2. Attention Flags Not Clearing

```bash
# Force clear stuck flags
devx session flag session-name --clear

# Check session metadata
cat ~/.config/devx/sessions.json
```

#### 3. Tmux Session Issues

```bash
# List all tmux sessions
tmux list-sessions

# Kill stuck session
tmux kill-session -t session-name

# Recreate session
devx session rm session-name
devx session create session-name
```

#### 4. Port Conflicts

```bash
# Check allocated ports
devx session list

# Remove conflicting session
devx session rm old-session

# Create new session with auto-allocation
devx session create new-session
```

#### 5. Environment Variables Not Loading

```bash
# Check if direnv is installed
which direnv

# Manually source environment
source .envrc

# Verify variables are set
echo $FE_PORT $API_PORT
```

### Debug Commands

```bash
# View all sessions with detailed info
devx session list

# Check session metadata
cat ~/.config/devx/sessions.json

# View tmux session content
tmux capture-pane -t session-name -p

# Check Caddy routes (if enabled)
devx caddy check
```

## Advanced Usage

### Custom AI Agent Integration

#### 1. Custom Pane Commands

Modify the session template to include your AI tools:

```yaml
panes:
  - claude --project {{.Name}}
  - chatgpt-cli --session {{.Name}}
  - copilot suggest
```

#### 2. Environment-Specific AI Configuration

```bash
# Set AI-specific environment variables
echo 'export OPENAI_API_KEY="your-key"' >> .envrc
echo 'export CLAUDE_API_KEY="your-key"' >> .envrc
```

#### 3. Automated AI Workflows

Create scripts that integrate with devx:

```bash
#!/bin/bash
# ai-workflow.sh

SESSION_NAME=$1
TASK_DESCRIPTION=$2

# Create session
devx session create "$SESSION_NAME"

# Run AI task
cd ~/.devx/sessions/"$SESSION_NAME"
claude "$TASK_DESCRIPTION"

# Flag when complete
devx session flag "$SESSION_NAME" "claude_completed_$TASK_DESCRIPTION"
```

### Integration with CI/CD

```bash
# In your CI pipeline
if devx session list | grep -q "attention_flag.*true"; then
  echo "Sessions need attention before deployment"
  exit 1
fi
```

## API Reference

### Session Flag Commands

```bash
# Set attention flag
devx session flag <name> [reason] [--force]

# Clear attention flag  
devx session flag <name> --clear

# List sessions with flags
devx session list
```

### Session Management

```bash
# Create session
devx session create <name> [--project <alias>] [--fe-port <port>] [--api-port <port>] [--no-tmux]

# Attach to session
devx session attach <name>

# Remove session
devx session rm <name>

# List all sessions
devx session list

# Clear all sessions
devx session clear
```

### Configuration

```bash
# View configuration
devx config

# Get specific value
devx config get <key>

# Set configuration value
devx config set <key> <value>

# Initialize configuration
devx config init
```

## Contributing

To improve AI agent integration with devx:

1. **Report Issues**: Share your AI workflow challenges
2. **Suggest Features**: Propose new AI integration features
3. **Share Templates**: Contribute session templates for different AI tools
4. **Document Patterns**: Share successful AI workflow patterns

## License

This documentation is part of the devx project and follows the same MIT license.
