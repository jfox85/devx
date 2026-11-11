package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const defaultTmuxpTemplate = `session_name: {{.Name}}
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
      - cd {{.Path}}{{range $name, $port := .Ports}}
      - export {{$name}}={{$port}}{{end}}
      - export SESSION_NAME={{.Name}}
    panes:
      - echo "Backend window - Session: {{.Name}}"
      - echo "Start your backend server here"

  - window_name: frontend
    layout: tiled
    shell_command_before:
      - cd {{.Path}}{{range $name, $port := .Ports}}
      - export {{$name}}={{$port}}{{end}}
      - export SESSION_NAME={{.Name}}
    panes:
      - echo "Frontend window - Session: {{.Name}}"
      - echo "Start your frontend server here"
`

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize devx configuration and tmuxp template",
	Long: `Create a starter config and tmuxp template without overwriting existing files.
The config defaults to ~/.config/devx/config.yaml; use --config to choose another path.`,
	RunE: runConfigInit,
}

func init() {
	configCmd.AddCommand(configInitCmd)
}

func runConfigInit(cmd *cobra.Command, args []string) error {
	// Create config directory
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(home, ".config", "devx")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, "config.yaml")
	if cfgFile != "" {
		configPath = cfgFile
	}
	// Start with portable defaults, never effective project settings or credentials.
	const starterConfig = `# DevX starter configuration. Other settings use built-in defaults.
basedomain: localhost
caddy_api: http://localhost:2019
tmuxp_template: ~/.config/devx/session.yaml.tmpl
ports:
  - ui
  - api
# Set true for worktrees without local proxy routing.
disable_caddy: false
`
	if err := writeInitialConfigFile(cmd, configPath, []byte(starterConfig), 0600); err != nil {
		return err
	}
	templatePath := filepath.Join(configDir, "session.yaml.tmpl")
	return writeInitialConfigFile(cmd, templatePath, []byte(defaultTmuxpTemplate), 0644)
}

// Exclusive creation makes repeated initialization safe, including concurrent runs.
func writeInitialConfigFile(cmd *cobra.Command, path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, os.ErrExist) {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("inspect %s: %w", path, statErr)
		}
		if info.IsDir() {
			return fmt.Errorf("expected a config file, found directory at %s", path)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Kept existing file at %s\n", path)
		return nil
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := writeAndCloseInitialConfigFile(path, file, content); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", path)
	return nil
}

// Discard only a newly created file on failure, so initialization can be retried.
func writeAndCloseInitialConfigFile(path string, file io.WriteCloser, content []byte) error {
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
