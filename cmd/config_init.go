package cmd

import (
	"fmt"
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
	Short: "Initialize devx configuration with default template",
	Long:  `Create the default tmuxp template file that can be customized.`,
	RunE:  runConfigInit,
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
	
	// Create template file
	templatePath := filepath.Join(configDir, "session.yaml.tmpl")
	
	// Check if file already exists
	if _, err := os.Stat(templatePath); err == nil {
		fmt.Printf("Template file already exists at %s\n", templatePath)
		fmt.Printf("Use --force to overwrite\n")
		return nil
	}
	
	// Write default template
	if err := os.WriteFile(templatePath, []byte(defaultTmuxpTemplate), 0644); err != nil {
		return fmt.Errorf("failed to write template file: %w", err)
	}
	
	fmt.Printf("Created default tmuxp template at %s\n", templatePath)
	fmt.Printf("You can now customize this template to suit your workflow.\n")
	
	return nil
}