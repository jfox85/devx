package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jfox85/devx/session"
)

func TestValidateManualWorktreeRemovalAllowsManagedWorktree(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, ".worktrees", "safe")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "safe", ProjectPath: project, Path: path}
	if err := validateManualWorktreeRemoval(sess); err != nil {
		t.Fatalf("expected managed worktree path to validate: %v", err)
	}
}

func TestValidateManualWorktreeRemovalRejectsUnmanagedPath(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	sess := &session.Session{Name: "unsafe", ProjectPath: project, Path: outside}
	if err := validateManualWorktreeRemoval(sess); err == nil {
		t.Fatal("expected unmanaged path to be rejected")
	}
}
