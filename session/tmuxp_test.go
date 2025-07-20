package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateTmuxpConfig(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "devx-tmuxp-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Ensure we use the default template by temporarily unsetting any custom template
	oldTemplate := os.Getenv("DEVX_TMUXP_TEMPLATE")
	os.Unsetenv("DEVX_TMUXP_TEMPLATE")
	defer func() {
		if oldTemplate != "" {
			os.Setenv("DEVX_TMUXP_TEMPLATE", oldTemplate)
		}
	}()
	
	// Test data
	data := TmuxpData{
		Name:  "test-session",
		Path:  tmpDir,
		Ports: map[string]int{"ui": 3000, "api": 3001},
	}
	
	// Generate config
	if err := GenerateTmuxpConfig(tmpDir, data); err != nil {
		t.Fatalf("failed to generate tmuxp config: %v", err)
	}
	
	// Read generated file
	configPath := filepath.Join(tmpDir, ".tmuxp.yaml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	
	configStr := string(content)
	
	// Debug: print the generated config
	t.Logf("Generated config:\n%s", configStr)
	
	// Verify session name
	if !strings.Contains(configStr, "session_name: test-session") {
		t.Error("config should contain session name")
	}
	
	// Verify start directory
	if !strings.Contains(configStr, "start_directory: "+tmpDir) {
		t.Error("config should contain start directory")
	}
	
	// Verify at least one window exists
	if !strings.Contains(configStr, "windows:") {
		t.Error("config should contain windows section")
	}
	
	// Verify at least editor window exists (common to all templates)
	if !strings.Contains(configStr, "window_name: editor") {
		t.Error("config should contain editor window")
	}
	
	// Verify ports are referenced in some way (could be as env vars or inline)
	if !strings.Contains(configStr, "3000") {
		t.Error("config should reference UI port (3000)")
	}
	if !strings.Contains(configStr, "3001") {
		t.Error("config should reference API port (3001)")
	}
	
	// Verify session name env var
	if !strings.Contains(configStr, "SESSION_NAME=test-session") {
		t.Error("config should contain session name env var")
	}
}

func TestIsTmuxRunning(t *testing.T) {
	// Save original TMUX env var
	originalTmux := os.Getenv("TMUX")
	defer os.Setenv("TMUX", originalTmux)
	
	// Test when not in tmux
	os.Unsetenv("TMUX")
	if IsTmuxRunning() {
		t.Error("should return false when TMUX env var is not set")
	}
	
	// Test when in tmux
	os.Setenv("TMUX", "/tmp/tmux-123/default,456,0")
	if !IsTmuxRunning() {
		t.Error("should return true when TMUX env var is set")
	}
}

func TestLoadTmuxpTemplateFromFile(t *testing.T) {
	// Create temp directory for custom template
	tmpDir, err := os.MkdirTemp("", "devx-template-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	
	// Save original HOME env var
	originalHome := os.Getenv("HOME")
	defer os.Setenv("HOME", originalHome)
	
	// Set HOME to temp directory
	os.Setenv("HOME", tmpDir)
	
	// Create config directory and custom template
	configDir := filepath.Join(tmpDir, ".config", "devx")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	
	customTemplate := `session_name: {{.Name}}
custom: true
windows:
  - window_name: custom_window
    panes:
      - echo "Custom template loaded for {{.Name}}"
`
	
	templatePath := filepath.Join(configDir, "session.yaml.tmpl")
	if err := os.WriteFile(templatePath, []byte(customTemplate), 0644); err != nil {
		t.Fatalf("failed to write custom template: %v", err)
	}
	
	// Test data
	data := TmuxpData{
		Name:  "test-custom",
		Path:  tmpDir,
		Ports: map[string]int{"FE_PORT": 3000, "API_PORT": 3001},
	}
	
	// Generate config using custom template
	if err := GenerateTmuxpConfig(tmpDir, data); err != nil {
		t.Fatalf("failed to generate tmuxp config: %v", err)
	}
	
	// Read generated file
	configPath := filepath.Join(tmpDir, ".tmuxp.yaml")
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read generated config: %v", err)
	}
	
	configStr := string(content)
	
	// Verify custom template was used
	if !strings.Contains(configStr, "custom: true") {
		t.Error("custom template should contain 'custom: true'")
	}
	
	if !strings.Contains(configStr, "custom_window") {
		t.Error("custom template should contain custom_window")
	}
	
	if !strings.Contains(configStr, "Custom template loaded for test-custom") {
		t.Error("custom template should contain rendered custom text")
	}
}