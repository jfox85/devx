package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseWorktreeOutput(t *testing.T) {
	output := []byte(`worktree /path/to/repo
HEAD abc123def456
branch refs/heads/main

worktree /path/to/repo/.worktrees/feature
HEAD 789xyz123
branch refs/heads/feature
`)

	worktrees := parseWorktreeOutput(output)

	if len(worktrees) != 2 {
		t.Fatalf("expected 2 worktrees, got %d", len(worktrees))
	}

	// Check first worktree
	if worktrees[0].Path != "/path/to/repo" {
		t.Errorf("expected path '/path/to/repo', got %s", worktrees[0].Path)
	}
	if worktrees[0].Branch != "main" {
		t.Errorf("expected branch 'main', got %s", worktrees[0].Branch)
	}
	if worktrees[0].Head != "abc123def456" {
		t.Errorf("expected head 'abc123def456', got %s", worktrees[0].Head)
	}

	// Check second worktree
	if worktrees[1].Path != "/path/to/repo/.worktrees/feature" {
		t.Errorf("expected path '/path/to/repo/.worktrees/feature', got %s", worktrees[1].Path)
	}
	if worktrees[1].Branch != "feature" {
		t.Errorf("expected branch 'feature', got %s", worktrees[1].Branch)
	}
}

// initGitRepoWithRemote initialises a bare git repo and a clone for testing remote operations.
// Returns (bareDir, cloneDir, runGit). Both directories are cleaned up via t.Cleanup.
func initGitRepoWithRemote(t *testing.T) (bareDir, cloneDir string, runGit func(string, ...string)) {
	t.Helper()

	bareDir = t.TempDir()
	cloneDir = t.TempDir()

	runGit = func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
		}
	}

	// Create a bare repo that will serve as origin
	runGit(bareDir, "init", "--bare")

	// Clone it to get a working directory
	runGit(cloneDir, "clone", bareDir, ".")

	// Configure identity so commits work
	runGit(cloneDir, "config", "user.email", "test@test.com")
	runGit(cloneDir, "config", "user.name", "Test")

	// Create an initial commit so the repo is non-empty
	initFile := filepath.Join(cloneDir, "README.md")
	if err := os.WriteFile(initFile, []byte("init"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(cloneDir, "add", ".")
	runGit(cloneDir, "commit", "-m", "initial")
	runGit(cloneDir, "push", "origin", "HEAD")

	return bareDir, cloneDir, runGit
}

func TestRemoteBranchExists_False(t *testing.T) {
	_, cloneDir, _ := initGitRepoWithRemote(t)

	exists, err := RemoteBranchExists(cloneDir, "nonexistent-branch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Error("expected RemoteBranchExists to return false for nonexistent branch")
	}
}

func TestRemoteBranchExists_True(t *testing.T) {
	_, cloneDir, runGit := initGitRepoWithRemote(t)

	// Create a branch and push it to origin
	runGit(cloneDir, "checkout", "-b", "my-feature")
	runGit(cloneDir, "push", "origin", "my-feature")

	// Fetch so remote refs are visible in the clone
	runGit(cloneDir, "fetch", "origin")

	exists, err := RemoteBranchExists(cloneDir, "my-feature")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Error("expected RemoteBranchExists to return true for branch pushed to origin")
	}
}

func TestParseWorktreeOutputDetached(t *testing.T) {
	// Test parsing detached HEAD
	output := []byte(`worktree /path/to/repo
HEAD abc123def456
detached

`)

	worktrees := parseWorktreeOutput(output)

	if len(worktrees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(worktrees))
	}

	if worktrees[0].Branch != "" {
		t.Errorf("expected empty branch for detached HEAD, got %s", worktrees[0].Branch)
	}
}
