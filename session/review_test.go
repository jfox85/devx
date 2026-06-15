package session

import (
	"os"
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

func TestRefreshSessionReviewStaleDetectsStatusChange(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := initReviewRepo(t)
	store, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Mutate(func(fresh *SessionStore) error {
		fresh.Sessions["test"] = &Session{Name: "test", Branch: "feature", Path: repo}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	review, err := ReviewSession(store.Sessions["test"], ReviewOptions{BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := PersistSessionReview("test", review); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err = LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	updated, err := RefreshSessionReviewStale("test", store.Sessions["test"])
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || !updated.Stale {
		t.Fatalf("expected stale review, got %#v", updated)
	}
}

func TestRemoveSessionReviewDetails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	review := &SessionReview{Classification: ReviewClassificationDirtyOnly, Details: "secret-ish details"}
	if err := SaveSessionReviewDetails("test", review); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSessionReviewDetails("test"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSessionReviewDetails("test"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSessionReviewDetails("test"); !os.IsNotExist(err) {
		t.Fatalf("expected details to be removed, got %v", err)
	}
}

func TestMergeReviewDetailsKeepsMetadataStaleAndCounts(t *testing.T) {
	summary := &SessionReview{
		Classification:    ReviewClassificationDirtyOnly,
		Summary:           "metadata summary",
		Stale:             true,
		UniqueCommitCount: 2,
		DirtyFileCount:    1,
	}
	details := &SessionReview{
		Classification: ReviewClassificationClean,
		Summary:        "old detail summary",
		Stale:          false,
		Details:        "agent details",
		UniqueCommits:  []string{"a", "b"},
	}
	merged := MergeReviewDetails(summary, details)
	if !merged.Stale || merged.Classification != ReviewClassificationDirtyOnly || merged.UniqueCommitCount != 2 {
		t.Fatalf("metadata fields were not preserved: %#v", merged)
	}
	if merged.Details != "agent details" || len(merged.UniqueCommits) != 2 {
		t.Fatalf("detail fields were not merged: %#v", merged)
	}
}

func TestCompactSessionReviewRemovesDetailsButKeepsCounts(t *testing.T) {
	review := &SessionReview{
		Classification: ReviewClassificationDirtyOnly,
		Details:        "agent output",
		UniqueCommits:  []string{"abc commit"},
		DirtyFiles:     []string{"M file.txt"},
		UntrackedFiles: []string{"scratch.txt"},
	}
	compact := CompactSessionReview(review)
	if compact.Details != "" || len(compact.UniqueCommits) != 0 || len(compact.DirtyFiles) != 0 {
		t.Fatalf("compact review retained rich details: %#v", compact)
	}
	if compact.UniqueCommitCount != 1 || compact.DirtyFileCount != 1 || compact.UntrackedFileCount != 1 {
		t.Fatalf("compact counts not preserved: %#v", compact)
	}
}

func TestClassifyAgentTextProbablySafeBeforeSafe(t *testing.T) {
	got := classifyAgentText("This is probably safe to delete after checking logs.", ReviewClassificationNeedsReview)
	if got != ReviewClassificationProbablySafe {
		t.Fatalf("classification = %q", got)
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
