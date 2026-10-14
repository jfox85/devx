package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTmuxSessionLaunch(t *testing.T) {
	// Skip if tmux or tmuxp not available
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	if _, err := exec.LookPath("tmuxp"); err != nil {
		t.Skip("tmuxp not available")
	}

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "devx-tmux-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sessionName := "test-tmux-session"

	// Generate tmuxp config
	data := TmuxpData{
		Name:  sessionName,
		Path:  tmpDir,
		Ports: map[string]int{"ui": 3000, "api": 3001},
	}

	if err := GenerateTmuxpConfig(tmpDir, data, ""); err != nil {
		t.Fatalf("failed to generate tmuxp config: %v", err)
	}

	// Load session in detached mode
	tmuxpPath := filepath.Join(tmpDir, ".tmuxp.yaml")
	cmd := exec.Command("tmuxp", "load", "-d", tmuxpPath, "-s", sessionName)
	cmd.Dir = tmpDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to load tmuxp session: %v\nOutput: %s", err, output)
	}

	// Cleanup: kill the session after test
	defer func() {
		_ = exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	}()

	// Verify session exists
	cmd = exec.Command("tmux", "list-sessions")
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("failed to list tmux sessions: %v", err)
	}

	if !strings.Contains(string(output), sessionName) {
		t.Errorf("expected session '%s' to exist in tmux sessions", sessionName)
	}

	// Verify session has at least 1 window
	cmd = exec.Command("tmux", "list-windows", "-t", sessionName)
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("failed to list windows: %v", err)
	}

	windows := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(windows) < 1 {
		t.Errorf("expected at least 1 window, got %d", len(windows))
	}

	// Verify at least the editor window exists (common to all templates)
	windowOutput := string(output)
	if !strings.Contains(windowOutput, "editor") {
		t.Error("expected window 'editor' to exist")
	}
}

// Regression: a single-window template without base-index puts its only window
// at index 0. Restoring must not kill that window (which destroys the session).
func TestEnsureTmuxSessionKeepsSingleWindowAtIndexZero(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	if _, err := exec.LookPath("tmuxp"); err != nil {
		t.Skip("tmuxp not available")
	}

	tmpDir := t.TempDir()
	sessionName := "devx-test-single-window-restore"
	config := "session_name: " + sessionName + "\n" +
		"start_directory: " + tmpDir + "\n" +
		"windows:\n" +
		"  - window_name: primary\n" +
		"    panes:\n" +
		"      - bash\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".tmuxp.yaml"), []byte(config), 0644); err != nil {
		t.Fatalf("failed to write tmuxp config: %v", err)
	}
	_ = exec.Command("tmux", "kill-session", "-t", "="+sessionName).Run()
	t.Cleanup(func() {
		_ = exec.Command("tmux", "kill-session", "-t", "="+sessionName).Run()
	})

	if err := EnsureTmuxSession(sessionName, tmpDir); err != nil {
		t.Fatalf("EnsureTmuxSession failed: %v", err)
	}

	output, err := exec.Command("tmux", "list-windows", "-t", "="+sessionName, "-F", "#{window_name}").Output()
	if err != nil {
		t.Fatalf("failed to list windows: %v", err)
	}
	if got := strings.TrimSpace(string(output)); got != "primary" {
		t.Errorf("expected only window 'primary', got %q", got)
	}
}
