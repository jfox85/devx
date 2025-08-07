package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initTempRepo(t *testing.T) string {
	// Create temporary directory
	tmpDir, err := os.MkdirTemp("", "devx-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Create initial commit
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	// Create a file and commit
	testFile := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test"), 0644); err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("failed to create test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})

	return tmpDir
}

func TestCreateSession(t *testing.T) {
	repo := initTempRepo(t)

	// Change to the repo directory for the test
	oldDir, _ := os.Getwd()
	os.Chdir(repo)
	defer os.Chdir(oldDir)

	// Clean up any existing session first
	rootCmd.SetArgs([]string{"session", "rm", "feat-foo", "-f"})
	_ = rootCmd.Execute()

	// Cleanup session after test
	t.Cleanup(func() {
		rootCmd.SetArgs([]string{"session", "rm", "feat-foo", "-f"})
		_ = rootCmd.Execute()
	})

	// Run devx session create with --no-tmux to avoid launching tmux
	rootCmd.SetArgs([]string{"session", "create", "feat-foo", "--no-tmux"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("failed to execute command: %v", err)
	}

	// Assert worktree path exists
	worktreePath := filepath.Join(repo, ".worktrees", "feat-foo")
	if _, err := os.Stat(worktreePath); err != nil {
		t.Errorf("expected worktree path to exist: %v", err)
	}

	// Verify branch name in worktree
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = worktreePath
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to get branch name: %v", err)
	}

	branchName := string(output)
	branchName = branchName[:len(branchName)-1] // Remove newline
	if branchName != "feat-foo" {
		t.Errorf("expected branch name 'feat-foo', got '%s'", branchName)
	}
}

func TestCreateSessionTwice(t *testing.T) {
	repo := initTempRepo(t)

	// Change to the repo directory for the test
	oldDir, _ := os.Getwd()
	os.Chdir(repo)
	defer os.Chdir(oldDir)

	// Clean up any existing session first
	rootCmd.SetArgs([]string{"session", "rm", "feat-bar", "-f"})
	_ = rootCmd.Execute()

	// Cleanup session after test
	t.Cleanup(func() {
		rootCmd.SetArgs([]string{"session", "rm", "feat-bar", "-f"})
		_ = rootCmd.Execute()
	})

	// Create session first time with --no-tmux
	rootCmd.SetArgs([]string{"session", "create", "feat-bar", "--no-tmux"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("failed to execute command: %v", err)
	}

	// Try to create same session again
	rootCmd.SetArgs([]string{"session", "create", "feat-bar", "--no-tmux"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when creating duplicate session")
	}

	if err.Error() != "session 'feat-bar' already exists" {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestTmuxpConfigGeneration(t *testing.T) {
	repo := initTempRepo(t)

	// Change to the repo directory for the test
	oldDir, _ := os.Getwd()
	os.Chdir(repo)
	defer os.Chdir(oldDir)

	// Clean up any existing session first
	rootCmd.SetArgs([]string{"session", "rm", "feat-tmuxp", "-f"})
	_ = rootCmd.Execute()

	// Cleanup session after test
	t.Cleanup(func() {
		rootCmd.SetArgs([]string{"session", "rm", "feat-tmuxp", "-f"})
		_ = rootCmd.Execute()
	})

	// Run devx session create with --no-tmux to avoid tmux launching
	rootCmd.SetArgs([]string{"session", "create", "feat-tmuxp", "--no-tmux"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("failed to execute command: %v", err)
	}

	// Check that .tmuxp.yaml was created
	tmuxpPath := filepath.Join(repo, ".worktrees", "feat-tmuxp", ".tmuxp.yaml")
	if _, err := os.Stat(tmuxpPath); err != nil {
		t.Errorf("expected .tmuxp.yaml to exist: %v", err)
	}

	// Read and verify content
	content, err := os.ReadFile(tmuxpPath)
	if err != nil {
		t.Fatalf("failed to read .tmuxp.yaml: %v", err)
	}

	configStr := string(content)
	if !strings.Contains(configStr, "session_name: feat-tmuxp") {
		t.Error("tmuxp config should contain session name")
	}

	// Verify at least editor window exists (common to all templates)
	if !strings.Contains(configStr, "window_name: editor") {
		t.Error("tmuxp config should contain editor window")
	}

	// Verify windows section exists
	if !strings.Contains(configStr, "windows:") {
		t.Error("tmuxp config should contain windows section")
	}
}
