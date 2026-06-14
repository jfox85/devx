package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReviewSessionClassifiesCleanDirtyAndUniqueCommits(t *testing.T) {
	repo := initReviewRepo(t)
	sess := &Session{Name: "test", Branch: "feature", Path: repo}

	review, err := ReviewSession(sess, ReviewOptions{BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if review.Classification != ReviewClassificationClean {
		t.Fatalf("clean classification = %q", review.Classification)
	}

	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	review, err = ReviewSession(sess, ReviewOptions{BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if review.Classification != ReviewClassificationDirtyOnly {
		t.Fatalf("dirty classification = %q", review.Classification)
	}
	if len(review.UntrackedFiles) == 0 {
		t.Fatalf("expected untracked files")
	}

	runGit(t, repo, "add", "scratch.txt")
	runGit(t, repo, "commit", "-m", "feature work")
	review, err = ReviewSession(sess, ReviewOptions{BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if review.Classification != ReviewClassificationUniqueCommits {
		t.Fatalf("unique classification = %q", review.Classification)
	}
	if len(review.UniqueCommits) == 0 {
		t.Fatalf("expected unique commits")
	}
}

func TestReviewSessionMissingWorktree(t *testing.T) {
	review, err := ReviewSession(&Session{Name: "missing", Path: filepath.Join(t.TempDir(), "missing")}, ReviewOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if review.Classification != ReviewClassificationMissingWorktree {
		t.Fatalf("classification = %q", review.Classification)
	}
}

func initReviewRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial")
	runGit(t, dir, "checkout", "-b", "feature")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
