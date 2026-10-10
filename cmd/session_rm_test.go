package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
)

func TestAgentsToRetireMatchesExactInstanceOnly(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	sess := &session.Session{Name: "s", InstanceID: "si_aaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: t0}
	agents := []*piagent.Agent{
		{ID: "pa_000000000001", DevxSession: "s", SessionInstanceID: "si_aaaaaaaaaaaaaaaaaaaaaaaa"}, // bound, marker dropped
		{ID: "pa_000000000002", DevxSession: "s", SessionInstanceID: "si_bbbbbbbbbbbbbbbbbbbbbbbb"}, // earlier instance
		{ID: "pa_000000000003", DevxSession: "s"},                                                   // unbound legacy: only via marker
		{ID: "pa_000000000004", DevxSession: "other", SessionInstanceID: "si_aaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	got := agentsToRetire("s", sess, "pa_000000000009", agents)
	if strings.Join(got, ",") != "pa_000000000009,pa_000000000001" {
		t.Fatalf("got %v", got)
	}
	// Id-less record (old writer): exact created_at witness only.
	sess2 := &session.Session{Name: "s", CreatedAt: t0}
	agents[0].SessionCreatedAt = t0
	agents[1].SessionCreatedAt = t0.Add(time.Nanosecond)
	if got := agentsToRetire("s", sess2, "", agents); strings.Join(got, ",") != "pa_000000000001" {
		t.Fatalf("id-less: got %v", got)
	}
}

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

func TestRemoveGatepostStateDirRejectsSymlinkStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".local", "share", "devx", "gatepost")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(root, "demo")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "demo"}
	sess.Target.Gatepost.SessionDir = link
	if err := removeGatepostStateDir(sess); err == nil {
		t.Fatalf("expected symlinked Gatepost state dir to be rejected")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside target should remain: %v", err)
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
