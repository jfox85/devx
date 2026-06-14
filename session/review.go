package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var controlSeqRE = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

const (
	ReviewClassificationClean           = "clean"
	ReviewClassificationDirtyOnly       = "dirty-only"
	ReviewClassificationUniqueCommits   = "unique-commits"
	ReviewClassificationMissingWorktree = "missing-worktree"
	ReviewClassificationNeedsReview     = "needs-human-review"
	ReviewClassificationPreserve        = "preserve-before-delete"
	ReviewClassificationProbablySafe    = "probably-safe-to-delete"
	ReviewClassificationSafe            = "safe-to-delete"
	ReviewClassificationError           = "error"
)

// SessionReview is a persisted snapshot of a session/worktree cleanup review.
// It is intentionally advisory: devx never deletes sessions based on this data.
type SessionReview struct {
	BaseBranch     string    `json:"base_branch,omitempty"`
	HeadSHA        string    `json:"head_sha,omitempty"`
	StatusHash     string    `json:"status_hash,omitempty"`
	ReviewedAt     time.Time `json:"reviewed_at,omitempty"`
	Harness        string    `json:"harness,omitempty"`
	Classification string    `json:"classification"`
	Summary        string    `json:"summary,omitempty"`
	Details        string    `json:"details,omitempty"`
	Stale          bool      `json:"stale,omitempty"`
	Error          string    `json:"error,omitempty"`

	UniqueCommits  []string `json:"unique_commits,omitempty"`
	ChangedFiles   []string `json:"changed_files,omitempty"`
	DirtyFiles     []string `json:"dirty_files,omitempty"`
	UntrackedFiles []string `json:"untracked_files,omitempty"`

	UniqueCommitCount  int  `json:"unique_commit_count,omitempty"`
	ChangedFileCount   int  `json:"changed_file_count,omitempty"`
	DirtyFileCount     int  `json:"dirty_file_count,omitempty"`
	UntrackedFileCount int  `json:"untracked_file_count,omitempty"`
	Truncated          bool `json:"truncated,omitempty"`
}

type ReviewOptions struct {
	BaseBranch string
	MaxFiles   int
}

func ReviewSession(sess *Session, opts ReviewOptions) (*SessionReview, error) {
	if sess == nil {
		return nil, errors.New("session is nil")
	}
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 80
	}
	review := &SessionReview{ReviewedAt: time.Now(), Classification: ReviewClassificationError}
	if sess.Path == "" {
		review.Classification = ReviewClassificationMissingWorktree
		review.Error = "session has no worktree path"
		review.Summary = "Worktree is missing; only stale metadata remains."
		return review, nil
	}
	if info, err := os.Stat(sess.Path); err != nil || !info.IsDir() {
		review.Classification = ReviewClassificationMissingWorktree
		review.Error = "worktree path does not exist"
		review.Summary = "Worktree path is missing; session metadata may be stale."
		return review, nil
	}

	base, err := ResolveReviewBase(sess.Path, opts.BaseBranch)
	if err != nil {
		review.Error = err.Error()
		review.Summary = "Unable to resolve a base branch for review."
		return review, nil
	}
	review.BaseBranch = base

	if head, err := gitOutput(sess.Path, "rev-parse", "HEAD"); err == nil {
		review.HeadSHA = strings.TrimSpace(head)
	}
	status, err := gitOutput(sess.Path, "status", "--porcelain=v1")
	if err != nil {
		review.Error = err.Error()
		review.Summary = "Unable to read git status."
		return review, nil
	}
	review.StatusHash = hashString(status)
	dirty, untracked := parsePorcelain(status, maxFiles)
	review.DirtyFiles = dirty.files
	review.UntrackedFiles = untracked.files
	review.Truncated = dirty.truncated || untracked.truncated

	commits, err := gitOutput(sess.Path, "log", "--oneline", base+"..HEAD")
	if err != nil {
		review.Error = err.Error()
		review.Summary = "Unable to compare commits against base branch."
		return review, nil
	}
	review.UniqueCommits = capLines(commits, maxFiles, &review.Truncated)
	files, err := gitOutput(sess.Path, "diff", "--name-status", base+"...HEAD")
	if err != nil {
		review.Error = err.Error()
		review.Summary = "Unable to compare changed files against base branch."
		return review, nil
	}
	review.ChangedFiles = capLines(files, maxFiles, &review.Truncated)
	review.setCounts()

	hasDirty := len(review.DirtyFiles) > 0 || len(review.UntrackedFiles) > 0
	hasCommits := len(review.UniqueCommits) > 0
	switch {
	case hasCommits:
		review.Classification = ReviewClassificationUniqueCommits
		review.Summary = fmt.Sprintf("%d unique commit(s) outside %s; review before cleanup.", len(review.UniqueCommits), base)
	case hasDirty:
		review.Classification = ReviewClassificationDirtyOnly
		review.Summary = fmt.Sprintf("No unique commits outside %s, but worktree has local changes/untracked files.", base)
	default:
		review.Classification = ReviewClassificationClean
		review.Summary = fmt.Sprintf("No unique commits or local changes relative to %s.", base)
	}
	return review, nil
}

func ResolveReviewBase(repoPath, requested string) (string, error) {
	candidates := []string{}
	if requested != "" {
		candidates = append(candidates, requested)
	} else {
		candidates = append(candidates, "origin/main", "main", "origin/master", "master")
	}
	for _, c := range candidates {
		if exec.Command("git", "-C", repoPath, "rev-parse", "--verify", c).Run() == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("could not resolve base branch (tried %s)", strings.Join(candidates, ", "))
}

func ReviewIsStale(sess *Session) bool {
	if sess == nil || sess.Review == nil || sess.Path == "" {
		return false
	}
	if _, err := os.Stat(sess.Path); err != nil {
		return true
	}
	head, err := gitOutput(sess.Path, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(head) != sess.Review.HeadSHA {
		return true
	}
	status, err := gitOutput(sess.Path, "status", "--porcelain=v1")
	if err != nil || hashString(status) != sess.Review.StatusHash {
		return true
	}
	return false
}

func PersistSessionReview(name string, review *SessionReview) error {
	if review != nil {
		review.setCounts()
		review.Stale = false
		if err := SaveSessionReviewDetails(name, review); err != nil {
			return err
		}
	}
	store, err := LoadSessions()
	if err != nil {
		return err
	}
	return store.UpdateSession(name, func(sess *Session) { sess.Review = CompactSessionReview(review) })
}

func ClearSessionReview(name string) error {
	store, err := LoadSessions()
	if err != nil {
		return err
	}
	_ = RemoveSessionReviewDetails(name)
	return store.UpdateSession(name, func(sess *Session) { sess.Review = nil })
}

func RemoveSessionReviewDetails(name string) error {
	err := os.Remove(reviewDetailsPath(name))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func RefreshSessionReviewStale(name string, sess *Session) (*SessionReview, error) {
	if sess == nil || sess.Review == nil {
		return nil, nil
	}
	stale := ReviewIsStale(sess)
	if sess.Review.Stale == stale {
		return sess.Review, nil
	}
	updated := *sess.Review
	updated.Stale = stale
	store, err := LoadSessions()
	if err != nil {
		return &updated, err
	}
	if err := store.UpdateSession(name, func(s *Session) { s.Review = &updated }); err != nil {
		return &updated, err
	}
	return &updated, nil
}

func CompactSessionReview(review *SessionReview) *SessionReview {
	if review == nil {
		return nil
	}
	copy := *review
	copy.setCounts()
	copy.Details = ""
	copy.UniqueCommits = nil
	copy.ChangedFiles = nil
	copy.DirtyFiles = nil
	copy.UntrackedFiles = nil
	return &copy
}

func (r *SessionReview) setCounts() {
	if r == nil {
		return
	}
	if len(r.UniqueCommits) > 0 {
		r.UniqueCommitCount = len(r.UniqueCommits)
	}
	if len(r.ChangedFiles) > 0 {
		r.ChangedFileCount = len(r.ChangedFiles)
	}
	if len(r.DirtyFiles) > 0 {
		r.DirtyFileCount = len(r.DirtyFiles)
	}
	if len(r.UntrackedFiles) > 0 {
		r.UntrackedFileCount = len(r.UntrackedFiles)
	}
}

func SaveSessionReviewDetails(name string, review *SessionReview) error {
	path := reviewDetailsPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func LoadSessionReviewDetails(name string) (*SessionReview, error) {
	data, err := os.ReadFile(reviewDetailsPath(name))
	if err != nil {
		return nil, err
	}
	var review SessionReview
	if err := json.Unmarshal(data, &review); err != nil {
		return nil, err
	}
	return &review, nil
}

func reviewDetailsPath(name string) string {
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(reviewDetailsDir(), hex.EncodeToString(sum[:])+".json")
}

func reviewDetailsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "devx", "reviews")
	}
	return filepath.Join(home, ".config", "devx", "reviews")
}

func BuildReviewPrompt(sess *Session, review *SessionReview) string {
	review.setCounts()
	b, _ := json.MarshalIndent(review, "", "  ")
	return fmt.Sprintf(`Review this devx session/worktree for cleanup.

Goal: decide whether there is valuable work worth preserving before the user deletes the session. Do not delete or modify anything.

Session: %s
Worktree: %s
Branch: %s

Deterministic review JSON:
%s

Return a concise human-facing answer with:
- recommendation: safe-to-delete, probably-safe-to-delete, needs-human-review, or preserve-before-delete
- one-line summary
- noteworthy files/commits
- risks or manual checks
`, sess.Name, sess.Path, sess.Branch, string(b))
}

func RunReviewHarness(ctx context.Context, sess *Session, review *SessionReview, harness string, command []string) (*SessionReview, error) {
	if len(command) == 0 {
		return nil, errors.New("harness command is empty")
	}
	prompt := BuildReviewPrompt(sess, review)
	tmpDir, err := os.MkdirTemp("", "devx-review-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	promptFile := filepath.Join(tmpDir, "prompt.md")
	if err := os.WriteFile(promptFile, []byte(prompt), 0o600); err != nil {
		return nil, err
	}
	args := make([]string, len(command))
	for i, a := range command {
		a = strings.ReplaceAll(a, "{prompt_file}", promptFile)
		args[i] = a
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = sess.Path
	out, err := cmd.CombinedOutput()
	updated := *review
	updated.Harness = harness
	updated.ReviewedAt = time.Now()
	updated.Details = truncateReviewText(cleanReviewText(string(out)), 8000)
	updated.Classification = classifyAgentText(updated.Details, review.Classification)
	if updated.Summary == "" || updated.Classification != review.Classification {
		updated.Summary = firstNonEmptyLine(updated.Details, review.Summary)
	}
	if err != nil {
		updated.Error = err.Error()
		return &updated, err
	}
	return &updated, nil
}

func classifyAgentText(text, fallback string) string {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "probably safe to delete") {
		return ReviewClassificationProbablySafe
	}
	for _, c := range []string{ReviewClassificationPreserve, ReviewClassificationNeedsReview, ReviewClassificationProbablySafe, ReviewClassificationSafe} {
		if strings.Contains(lower, c) {
			return c
		}
	}
	if strings.Contains(lower, "preserve") || strings.Contains(lower, "worth keeping") {
		return ReviewClassificationPreserve
	}
	if strings.Contains(lower, "needs human") || strings.Contains(lower, "review before") {
		return ReviewClassificationNeedsReview
	}
	if strings.Contains(lower, "safe to delete") {
		return ReviewClassificationSafe
	}
	return fallback
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type cappedFiles struct {
	files     []string
	truncated bool
}

func parsePorcelain(status string, max int) (dirty cappedFiles, untracked cappedFiles) {
	for _, line := range strings.Split(strings.TrimRight(status, "\n"), "\n") {
		if line == "" {
			continue
		}
		if len(line) < 3 {
			continue
		}
		path := cleanReviewText(strings.TrimSpace(line[3:]))
		if strings.HasPrefix(line, "??") {
			untracked.add(path, max)
		} else {
			dirty.add(cleanReviewText(strings.TrimSpace(line)), max)
		}
	}
	sort.Strings(dirty.files)
	sort.Strings(untracked.files)
	return dirty, untracked
}

func (c *cappedFiles) add(s string, max int) {
	if len(c.files) >= max {
		c.truncated = true
		return
	}
	c.files = append(c.files, s)
}

func capLines(s string, max int, truncated *bool) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(lines) >= max {
			*truncated = true
			break
		}
		lines = append(lines, cleanReviewText(line))
	}
	return lines
}

func cleanReviewText(s string) string {
	s = controlSeqRE.ReplaceAllString(s, "")
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return r
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}

func truncateReviewText(s string, max int) string {
	if len(s) <= max {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(s[:max]) + "\n… truncated"
}

func firstNonEmptyLine(text, fallback string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if line != "" {
			return line
		}
	}
	return fallback
}
