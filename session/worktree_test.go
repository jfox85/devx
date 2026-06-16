package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestSessionWorkdirExistsStandaloneClone(t *testing.T) {
	// A standalone clone (used by containerized sessions) is NOT a registered
	// git worktree, so it must still be recognised as an existing workdir.
	_, cloneDir, _ := initGitRepoWithRemote(t)

	// The clone created by initGitRepoWithRemote is a self-contained repo whose
	// .git is a directory — exactly the shape CreateIndependentClone produces.
	// Pass a valid repo as the source (cloneDir itself) so the worktree-list
	// probe succeeds before the standalone-clone fallback is consulted.
	exists, err := SessionWorkdirExists(cloneDir, cloneDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("standalone clone should be reported as an existing session workdir")
	}
}

func TestSessionWorkdirExistsMissing(t *testing.T) {
	// A directory with no git checkout must not be reported as existing.
	_, cloneDir, _ := initGitRepoWithRemote(t)
	exists, err := SessionWorkdirExists(cloneDir, filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Fatal("non-existent workdir should not be reported as existing")
	}
}

func TestCreateIndependentCloneNewBranchOffMain(t *testing.T) {
	_, cloneDir, _ := initGitRepoWithRemote(t)

	wt, err := CreateIndependentClone(cloneDir, "jf-new-feature")
	if err != nil {
		t.Fatalf("CreateIndependentClone: %v", err)
	}

	// Standalone clone: .git is a real directory, not a worktree link file.
	info, err := os.Stat(filepath.Join(wt, ".git"))
	if err != nil || !info.IsDir() {
		t.Fatalf(".git should be a directory for a standalone clone; statErr=%v", err)
	}

	// On the session branch.
	out, err := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "jf-new-feature" {
		t.Fatalf("HEAD branch = %q, want jf-new-feature", got)
	}

	// Review diff against origin/main resolves (HEAD descends from main tip).
	if err := exec.Command("git", "-C", wt, "merge-base", "HEAD", "origin/main").Run(); err != nil {
		t.Fatalf("merge-base HEAD origin/main should resolve: %v", err)
	}
}

func TestCreateIndependentCloneExistingRemoteBranchTracksUpstream(t *testing.T) {
	_, cloneDir, runGit := initGitRepoWithRemote(t)

	// Publish a branch on origin that the session will adopt.
	runGit(cloneDir, "checkout", "-b", "jf-existing")
	if err := os.WriteFile(filepath.Join(cloneDir, "feature.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(cloneDir, "add", ".")
	runGit(cloneDir, "commit", "-m", "feature work")
	runGit(cloneDir, "push", "origin", "jf-existing")
	runGit(cloneDir, "checkout", "main")

	wt, err := CreateIndependentClone(cloneDir, "jf-existing")
	if err != nil {
		t.Fatalf("CreateIndependentClone: %v", err)
	}

	// Feature commit is present.
	if _, err := os.Stat(filepath.Join(wt, "feature.txt")); err != nil {
		t.Fatalf("existing-branch content missing: %v", err)
	}

	// Upstream tracking is configured so plain git pull/push work.
	out, err := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref",
		"--symbolic-full-name", "@{upstream}").Output()
	if err != nil {
		t.Fatalf("no upstream configured: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "origin/jf-existing" {
		t.Fatalf("upstream = %q, want origin/jf-existing", got)
	}
}

func TestCreateIndependentCloneLocalOnlyRepo(t *testing.T) {
	// A repo with no origin must still produce a working session clone.
	src := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t.com")
	run("config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	wt, err := CreateIndependentClone(src, "jf-local")
	if err != nil {
		t.Fatalf("CreateIndependentClone (local-only): %v", err)
	}
	out, err := exec.Command("git", "-C", wt, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "jf-local" {
		t.Fatalf("HEAD branch = %q, want jf-local", got)
	}
}

func TestCreateIndependentCloneDeepensForOldMergeBase(t *testing.T) {
	_, cloneDir, runGit := initGitRepoWithRemote(t)

	// Fork a feature branch at the very first commit, then advance main far
	// beyond the shallow depth (20) so the merge-base is below the horizon.
	runGit(cloneDir, "checkout", "-b", "jf-old")
	if err := os.WriteFile(filepath.Join(cloneDir, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(cloneDir, "add", ".")
	runGit(cloneDir, "commit", "-m", "old feature commit")
	runGit(cloneDir, "push", "origin", "jf-old")

	runGit(cloneDir, "checkout", "main")
	for i := 0; i < 30; i++ {
		if err := os.WriteFile(filepath.Join(cloneDir, "main.txt"),
			[]byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(cloneDir, "add", ".")
		runGit(cloneDir, "commit", "-m", "main advance")
	}
	runGit(cloneDir, "push", "origin", "main")

	wt, err := CreateIndependentClone(cloneDir, "jf-old")
	if err != nil {
		t.Fatalf("CreateIndependentClone: %v", err)
	}

	// After deepen-on-demand, the review base must resolve.
	if err := exec.Command("git", "-C", wt, "merge-base", "HEAD", "origin/main").Run(); err != nil {
		t.Fatalf("merge-base HEAD origin/main should resolve after deepening: %v", err)
	}
}
