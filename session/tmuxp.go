package session

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/jfox85/devx/config"
)

const tmuxpTemplate = `session_name: {{.Name}}
start_directory: {{.Path}}
windows:
  - window_name: editor
    layout: tiled
    shell_command_before:
      - cd {{.Path}}
    panes:
      - echo "Editor window - Session: {{.Name}}"
      - echo "Use your preferred editor here"

  - window_name: backend
    layout: tiled
    shell_command_before:
      - cd {{.Path}}{{range $serviceName, $port := .Ports}}
      - export {{toPortVar $serviceName}}={{$port}}{{end}}{{if .Routes}}{{range $serviceName, $hostname := .Routes}}
      - export {{toHostVar $serviceName}}=https://{{$hostname}}{{end}}{{end}}
      - export SESSION_NAME={{.Name}}
    panes:
      - echo "Backend window - Session: {{.Name}}"
      - echo "Start your backend server here"

  - window_name: frontend
    layout: tiled
    shell_command_before:
      - cd {{.Path}}{{range $serviceName, $port := .Ports}}
      - export {{toPortVar $serviceName}}={{$port}}{{end}}{{if .Routes}}{{range $serviceName, $hostname := .Routes}}
      - export {{toHostVar $serviceName}}=https://{{$hostname}}{{end}}{{end}}
      - export SESSION_NAME={{.Name}}
    panes:
      - echo "Frontend window - Session: {{.Name}}"
      - echo "Start your frontend server here"
`

type TmuxpData struct {
	Name   string
	Path   string
	Ports  map[string]int    // service name -> port number
	Routes map[string]string // service name -> hostname
}

// GenerateTmuxpConfig creates a .tmuxp.yaml file in the worktree directory
func GenerateTmuxpConfig(worktreePath string, data TmuxpData) error {
	templateContent, err := loadTmuxpTemplate()
	if err != nil {
		return fmt.Errorf("failed to load tmuxp template: %w", err)
	}

	// Create template with helper functions
	funcMap := template.FuncMap{
		"toPortVar": func(serviceName string) string {
			// Convert service name to PORT variable name
			// e.g., "ui" -> "UI_PORT", "auth-service" -> "AUTH_SERVICE_PORT"
			upper := strings.ToUpper(serviceName)
			return strings.ReplaceAll(upper, "-", "_") + "_PORT"
		},
		"toHostVar": func(serviceName string) string {
			// Convert service name to HOST variable name
			// e.g., "ui" -> "UI_HOST", "auth-service" -> "AUTH_SERVICE_HOST"
			upper := strings.ToUpper(serviceName)
			return strings.ReplaceAll(upper, "-", "_") + "_HOST"
		},
	}

	tmpl, err := template.New("tmuxp").Funcs(funcMap).Parse(templateContent)
	if err != nil {
		return fmt.Errorf("failed to parse tmuxp template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute tmuxp template: %w", err)
	}

	tmuxpPath := filepath.Join(worktreePath, ".tmuxp.yaml")
	if err := os.WriteFile(tmuxpPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write .tmuxp.yaml file: %w", err)
	}

	return nil
}

// loadTmuxpTemplate loads the tmuxp template from file, with fallback to embedded template
func loadTmuxpTemplate() (string, error) {
	// Get template path from config (using project-level first, then global)
	templatePath := config.GetTmuxTemplatePath()

	// Try to load from file
	if templatePath != "" {
		// Expand ~ if present
		if templatePath[0] == '~' {
			home, err := os.UserHomeDir()
			if err == nil {
				templatePath = filepath.Join(home, templatePath[1:])
			}
		}

		if content, err := os.ReadFile(templatePath); err == nil {
			return string(content), nil
		}
	}

	// Fall back to embedded template
	return tmuxpTemplate, nil
}

// LaunchTmuxSession launches a tmux session using tmuxp
func LaunchTmuxSession(worktreePath, sessionName string) error {
	// Check if tmuxp is available
	if _, err := exec.LookPath("tmuxp"); err != nil {
		return fmt.Errorf("tmuxp not found in PATH. Install with: pip install tmuxp")
	}

	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found in PATH")
	}

	tmuxpConfigPath := filepath.Join(worktreePath, ".tmuxp.yaml")

	// Check if config exists
	if _, err := os.Stat(tmuxpConfigPath); os.IsNotExist(err) {
		return fmt.Errorf("tmuxp config not found at %s", tmuxpConfigPath)
	}

	// Kill any existing session first
	_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run()

	// Set default terminal size for new sessions
	_ = exec.Command("tmux", "set-option", "-g", "default-size", "120x40").Run()

	// Load the tmuxp session in detached mode
	cmd := exec.Command("tmuxp", "load", "-d", tmuxpConfigPath, "-s", sessionName)
	cmd.Dir = worktreePath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to load tmuxp session: %w\n%s", err, output)
	}

	// Wait a moment for tmuxp to fully load, then kill the initial window
	time.Sleep(500 * time.Millisecond)

	// Kill all windows with index 0 (the initial directory window)
	_ = exec.Command("tmux", "kill-window", "-t", sessionName+":0").Run()

	// Also try to kill any window that just shows the directory cd command
	_ = exec.Command("bash", "-c", fmt.Sprintf(`tmux list-windows -t %s -F "#{window_index}:#{window_name}" | grep "^0:" | cut -d: -f1 | xargs -I {} tmux kill-window -t %s:{} 2>/dev/null || true`, sessionName, sessionName)).Run()

	// Attach to the session
	cmd = exec.Command("tmux", "attach", "-t", sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run in the current terminal
	if err := cmd.Run(); err != nil {
		// If attach fails, it might be because we're not in a terminal
		// or the session doesn't exist. Don't treat this as fatal.
		fmt.Printf("Note: Could not attach to tmux session '%s': %v\n", sessionName, err)
		fmt.Printf("You can manually attach with: tmux attach -t %s\n", sessionName)
	}

	return nil
}

// IsTmuxRunning checks if we're already inside a tmux session
func IsTmuxRunning() bool {
	return os.Getenv("TMUX") != ""
}

// AttachTmuxSession attaches to an existing tmux session
func AttachTmuxSession(sessionName string) error {
	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux not found in PATH")
	}

	// Check if session exists
	cmd := exec.Command("tmux", "has-session", "-t", sessionName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux session '%s' does not exist", sessionName)
	}

	// Attach to the session
	cmd = exec.Command("tmux", "attach", "-t", sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run in the current terminal
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to attach to tmux session '%s': %w", sessionName, err)
	}

	return nil
}
