package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jfox85/devx/session"
)

func TestRemoveGatepostStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stateDir := filepath.Join(home, ".local", "share", "devx", "gatepost", "demo")
	if err := os.MkdirAll(filepath.Join(stateDir, "agent-home", ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "demo"}
	sess.Target.Gatepost.SessionDir = stateDir
	if err := removeGatepostStateDir(sess); err != nil {
		t.Fatalf("removeGatepostStateDir: %v", err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("state dir still exists or stat failed unexpectedly: %v", err)
	}
}

func TestRemoveGatepostStateDirRejectsOutsideRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sess := &session.Session{Name: "demo"}
	sess.Target.Gatepost.SessionDir = t.TempDir()
	if err := removeGatepostStateDir(sess); err == nil {
		t.Fatalf("expected outside Gatepost state dir to be rejected")
	}
}

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
