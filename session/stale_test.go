package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func makeStaleRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "initial")

	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)
	runGit(t, dir, "remote", "add", "origin", remote)
	runGit(t, dir, "push", "-u", "origin", "main")
	return dir
}

func makeLocalOnlyStaleRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, output)
	}
}

func staleTestSession(path string) *Session {
	old := time.Now().Add(-45 * 24 * time.Hour)
	return &Session{Name: "stale", Branch: "main", Path: path, CreatedAt: old, UpdatedAt: old, LastAttached: old}
}

func TestAnalyzeStaleSessionClean(t *testing.T) {
	dir := makeStaleRepo(t)
	status := AnalyzeStaleSession(staleTestSession(dir), 14*24*time.Hour)
	if status.Category != StaleCategoryClean {
		t.Fatalf("category = %s, want %s; reasons=%v", status.Category, StaleCategoryClean, status.Reasons)
	}
	if status.HasUncommitted || status.HasUntracked || status.HasUnpushedCommits || status.GitStatusUnknown {
		t.Fatalf("expected clean git state: %+v", status)
	}
}

func TestAnalyzeStaleSessionCleanLocalBranchWithoutUpstream(t *testing.T) {
	dir := makeStaleRepo(t)
	runGit(t, dir, "checkout", "-b", "local-clean")
	status := AnalyzeStaleSession(&Session{Name: "local-clean", Branch: "local-clean", Path: dir, CreatedAt: time.Now().Add(-45 * 24 * time.Hour)}, 14*24*time.Hour)
	if status.Category != StaleCategoryClean {
		t.Fatalf("category = %s, want %s; reasons=%v", status.Category, StaleCategoryClean, status.Reasons)
	}
}

func TestAnalyzeStaleSessionDirtyNeedsReview(t *testing.T) {
	dir := makeStaleRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	status := AnalyzeStaleSession(staleTestSession(dir), 14*24*time.Hour)
	if status.Category != StaleCategoryNeedsReview {
		t.Fatalf("category = %s, want %s; reasons=%v", status.Category, StaleCategoryNeedsReview, status.Reasons)
	}
	if !status.HasUncommitted {
		t.Fatalf("expected uncommitted changes: %+v", status)
	}
}

func TestAnalyzeStaleSessionUntrackedNeedsReview(t *testing.T) {
	dir := makeStaleRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("scratch\n"), 0644); err != nil {
		t.Fatal(err)
	}
	status := AnalyzeStaleSession(staleTestSession(dir), 14*24*time.Hour)
	if status.Category != StaleCategoryNeedsReview || !status.HasUntracked {
		t.Fatalf("expected untracked needs review: %+v", status)
	}
}

func TestAnalyzeStaleSessionIgnoredStillClean(t *testing.T) {
	dir := makeStaleRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".gitignore")
	runGit(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "ignore env")
	runGit(t, dir, "push")
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status := AnalyzeStaleSession(staleTestSession(dir), 14*24*time.Hour)
	if status.Category != StaleCategoryClean || !status.HasIgnored {
		t.Fatalf("expected ignored-only files to remain clean: %+v", status)
	}
}

func TestAnalyzeStaleSessionUnpushedNeedsReview(t *testing.T) {
	dir := makeStaleRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "feature")
	status := AnalyzeStaleSession(staleTestSession(dir), 14*24*time.Hour)
	if status.Category != StaleCategoryNeedsReview || !status.HasUnpushedCommits || status.UnpushedCommits != 1 {
		t.Fatalf("expected one unpushed commit needs review: %+v", status)
	}
}

func TestAnalyzeStaleSessionLocalOnlyDefaultBranchNeedsReview(t *testing.T) {
	dir := makeLocalOnlyStaleRepo(t)
	status := AnalyzeStaleSession(staleTestSession(dir), 14*24*time.Hour)
	if status.Category != StaleCategoryNeedsReview || !status.UnpushedStatusUnknown {
		t.Fatalf("expected local-only default branch to need review: %+v", status)
	}
}

func TestAnalyzeStaleSessionMissingWorktreeBroken(t *testing.T) {
	status := AnalyzeStaleSession(staleTestSession(filepath.Join(t.TempDir(), "missing")), 14*24*time.Hour)
	if status.Category != StaleCategoryBroken {
		t.Fatalf("category = %s, want %s; reasons=%v", status.Category, StaleCategoryBroken, status.Reasons)
	}
}

func TestFastStaleAnalysisDoesNotMarkCleanupCandidate(t *testing.T) {
	dir := makeStaleRepo(t)
	status := AnalyzeStaleSessionWithOptions(staleTestSession(dir), FastStaleAnalysisOptions(14*24*time.Hour))
	if status.Category != StaleCategoryClean || !status.PotentialCleanupCandidate || status.CleanupCandidate {
		t.Fatalf("expected fast analysis to mark only potential cleanup candidate: %+v", status)
	}
}

func TestDeriveSessionStatusPriority(t *testing.T) {
	dir := makeStaleRepo(t)
	sess := staleTestSession(dir)
	stale := AnalyzeStaleSession(sess, 14*24*time.Hour)
	stale.CleanupCandidate = true

	derived := DeriveSessionStatus(sess, stale, 0, 0)
	if derived.Primary != SessionStatusCleanupCandidate {
		t.Fatalf("primary = %s, want cleanup candidate", derived.Primary)
	}

	sess.AttentionFlag = true
	sess.AttentionReason = "needs input"
	derived = DeriveSessionStatus(sess, stale, 0, 3)
	if derived.Primary != SessionStatusAttention {
		t.Fatalf("attention should outrank unseen artifacts, got %s", derived.Primary)
	}

	sess.AttentionFlag = false
	derived = DeriveSessionStatus(sess, stale, 2, 1)
	if derived.Primary != SessionStatusUnseenArtifact {
		t.Fatalf("primary = %s, want unseen artifact", derived.Primary)
	}
}

func TestStaleThresholdDurationRejectsOutOfRangeDays(t *testing.T) {
	if _, err := StaleThresholdDuration(0); err == nil {
		t.Fatal("expected zero days to be rejected")
	}
	if _, err := StaleThresholdDuration(MaxStaleThresholdDays + 1); err == nil {
		t.Fatal("expected days above max to be rejected")
	}
	if got, err := StaleThresholdDuration(MaxStaleThresholdDays); err != nil || got != time.Duration(MaxStaleThresholdDays)*24*time.Hour {
		t.Fatalf("max days duration = %v, err=%v", got, err)
	}
}
