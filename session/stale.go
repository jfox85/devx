package session

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DefaultStaleThreshold = 14 * 24 * time.Hour
const MaxStaleThresholdDays = 3650

const (
	StaleCategoryActive      = "active"
	StaleCategoryClean       = "stale-clean"
	StaleCategoryNeedsReview = "stale-needs-review"
	StaleCategoryBroken      = "broken"
)

const (
	SessionStatusAttention        = "attention"
	SessionStatusUnseenArtifact   = "unseen_artifact"
	SessionStatusBrokenOrStale    = "broken_or_stale"
	SessionStatusDirty            = "dirty"
	SessionStatusActive           = "active"
	SessionStatusCleanupCandidate = "cleanup_candidate"
	SessionStatusIdle             = "idle"
)

type StaleAnalysisMode string

const (
	StaleAnalysisModeFastList            StaleAnalysisMode = "fast-list"
	StaleAnalysisModeCleanupVerification StaleAnalysisMode = "cleanup-verification"
)

// StaleAnalysisOptions selects an explicit stale-analysis mode. Fast-list mode
// is cheap enough for polling UI state and intentionally skips git probes;
// cleanup-verification mode performs git checks and is required for pruning.
type StaleAnalysisOptions struct {
	Threshold              time.Duration
	Mode                   StaleAnalysisMode
	MaxConcurrentGitChecks int
	tmuxStatuses           map[string]string
	includeGit             bool
	includeIgnored         bool
	includeUnpushed        bool
	ignoredBlocksCleanup   bool
	unknownUnpushedBlocks  bool
	unknownGitBlocks       bool
}

func FastStaleAnalysisOptions(threshold time.Duration) StaleAnalysisOptions {
	return StaleAnalysisOptions{Threshold: threshold, Mode: StaleAnalysisModeFastList}
}

func CleanupStaleAnalysisOptions(threshold time.Duration) StaleAnalysisOptions {
	return StaleAnalysisOptions{Threshold: threshold, Mode: StaleAnalysisModeCleanupVerification, MaxConcurrentGitChecks: 8}
}

func normalizeStaleAnalysisOptions(opts StaleAnalysisOptions) StaleAnalysisOptions {
	if opts.Mode == "" {
		opts.Mode = StaleAnalysisModeCleanupVerification
	}
	switch opts.Mode {
	case StaleAnalysisModeFastList:
		opts.includeGit = false
		opts.includeIgnored = false
		opts.includeUnpushed = false
		opts.ignoredBlocksCleanup = false
		opts.unknownUnpushedBlocks = false
		opts.unknownGitBlocks = false
	case StaleAnalysisModeCleanupVerification:
		opts.includeGit = true
		opts.includeIgnored = true
		opts.includeUnpushed = true
		opts.ignoredBlocksCleanup = false
		opts.unknownUnpushedBlocks = true
		opts.unknownGitBlocks = true
		if opts.MaxConcurrentGitChecks <= 0 {
			opts.MaxConcurrentGitChecks = 8
		}
	default:
		opts.Mode = StaleAnalysisModeCleanupVerification
		return normalizeStaleAnalysisOptions(opts)
	}
	return opts
}

// StaleStatus describes whether a session is old enough and safe enough for
// automated cleanup. Only stale-clean sessions are removed in bulk.
type StaleStatus struct {
	SessionName               string        `json:"session_name"`
	Category                  string        `json:"category"`
	LastActiveAt              time.Time     `json:"last_active_at"`
	LastReviewedAt            time.Time     `json:"last_reviewed_at,omitempty"`
	Age                       time.Duration `json:"-"`
	AgeSeconds                int64         `json:"age_seconds"`
	WorktreeExists            bool          `json:"worktree_exists"`
	TmuxStatus                string        `json:"tmux_status"`
	EditorStatus              string        `json:"editor_status"`
	HasUncommitted            bool          `json:"has_uncommitted"`
	HasUntracked              bool          `json:"has_untracked"`
	HasIgnored                bool          `json:"has_ignored"`
	HasUnpushedCommits        bool          `json:"has_unpushed_commits"`
	UnpushedCommits           int           `json:"unpushed_commits"`
	GitStatusUnknown          bool          `json:"git_status_unknown"`
	UnpushedStatusUnknown     bool          `json:"unpushed_status_unknown"`
	GitChecksIncomplete       bool          `json:"git_checks_incomplete"`
	CleanupCandidate          bool          `json:"cleanup_candidate"`
	PotentialCleanupCandidate bool          `json:"potential_cleanup_candidate"`
	Reasons                   []string      `json:"reasons"`
}

// StaleSummary groups analyzed sessions for CLI/API/UI consumers.
type StaleSummary struct {
	ThresholdDays int           `json:"threshold_days"`
	Total         int           `json:"total"`
	Active        int           `json:"active"`
	Clean         int           `json:"clean"`
	NeedsReview   int           `json:"needs_review"`
	Broken        int           `json:"broken"`
	Statuses      []StaleStatus `json:"statuses"`
}

// SessionStatusSummary is the compact, derived display status shared by API/UI.
type SessionStatusSummary struct {
	Primary          string    `json:"primary"`
	Color            string    `json:"color"`
	Label            string    `json:"label"`
	Badges           []string  `json:"badges,omitempty"`
	Reasons          []string  `json:"reasons,omitempty"`
	Priority         int       `json:"priority"`
	Dirty            bool      `json:"dirty"`
	UnseenArtifacts  int       `json:"unseen_artifacts,omitempty"`
	ArtifactCount    int       `json:"artifact_count,omitempty"`
	WorktreeExists   bool      `json:"worktree_exists"`
	TmuxStatus       string    `json:"tmux_status,omitempty"`
	EditorRunning    bool      `json:"editor_running,omitempty"`
	CleanupCandidate bool      `json:"cleanup_candidate"`
	ChecksComplete   bool      `json:"checks_complete"`
	CleanupVerified  bool      `json:"cleanup_verified"`
	CheckedAt        time.Time `json:"checked_at"`
}

func AnalyzeStaleSessions(store *SessionStore, threshold time.Duration) StaleSummary {
	return AnalyzeStaleSessionsWithOptions(store, CleanupStaleAnalysisOptions(threshold))
}

func AnalyzeStaleSessionsWithOptions(store *SessionStore, opts StaleAnalysisOptions) StaleSummary {
	opts = normalizeStaleAnalysisOptions(opts)
	threshold := normalizeThreshold(opts.Threshold)
	thresholdDays := int(threshold.Hours() / 24)
	if thresholdDays <= 0 {
		thresholdDays = 1
	}

	summary := StaleSummary{ThresholdDays: thresholdDays}
	if store == nil {
		return summary
	}

	names := make([]string, 0, len(store.Sessions))
	for name := range store.Sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	if opts.tmuxStatuses == nil {
		opts.tmuxStatuses = tmuxSessionStatuses()
	}

	statuses := make([]StaleStatus, len(names))
	if opts.includeGit && opts.MaxConcurrentGitChecks > 1 && len(names) > 1 {
		var wg sync.WaitGroup
		jobs := make(chan int)
		workers := opts.MaxConcurrentGitChecks
		if workers > len(names) {
			workers = len(names)
		}
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					statuses[i] = AnalyzeStaleSessionWithOptions(store.Sessions[names[i]], opts)
				}
			}()
		}
		for i := range names {
			jobs <- i
		}
		close(jobs)
		wg.Wait()
	} else {
		for i, name := range names {
			statuses[i] = AnalyzeStaleSessionWithOptions(store.Sessions[name], opts)
		}
	}

	for _, status := range statuses {
		summary.Statuses = append(summary.Statuses, status)
		summary.Total++
		switch status.Category {
		case StaleCategoryClean:
			summary.Clean++
		case StaleCategoryNeedsReview:
			summary.NeedsReview++
		case StaleCategoryBroken:
			summary.Broken++
		default:
			summary.Active++
		}
	}
	return summary
}

func AnalyzeStaleSession(sess *Session, threshold time.Duration) StaleStatus {
	return AnalyzeStaleSessionWithOptions(sess, CleanupStaleAnalysisOptions(threshold))
}

func AnalyzeStaleSessionWithOptions(sess *Session, opts StaleAnalysisOptions) StaleStatus {
	opts = normalizeStaleAnalysisOptions(opts)
	threshold := normalizeThreshold(opts.Threshold)
	status := probeStaleSession(sess, opts)
	return classifyStaleStatus(status, sess, opts, threshold)
}

func probeStaleSession(sess *Session, opts StaleAnalysisOptions) StaleStatus {
	if sess == nil {
		return StaleStatus{Category: StaleCategoryBroken, LastActiveAt: time.Now(), AgeSeconds: 0, Reasons: []string{"session metadata missing"}}
	}
	now := time.Now()
	lastActive := sessionLastActiveAt(sess)
	tmux := "none"
	if opts.tmuxStatuses != nil {
		if value, ok := opts.tmuxStatuses[sess.Name]; ok {
			tmux = value
		}
	} else {
		tmux = tmuxStatus(sess.Name)
	}
	status := StaleStatus{
		SessionName:    sess.Name,
		Category:       StaleCategoryActive,
		LastActiveAt:   lastActive,
		LastReviewedAt: sess.LastReviewedAt,
		Age:            now.Sub(lastActive),
		AgeSeconds:     int64(now.Sub(lastActive).Seconds()),
		TmuxStatus:     tmux,
		EditorStatus:   "stopped",
	}
	if sess.EditorPID > 0 && IsProcessRunning(sess.EditorPID) {
		status.EditorStatus = "running"
	}

	if _, err := os.Stat(sess.Path); err == nil {
		status.WorktreeExists = true
	} else if os.IsNotExist(err) {
		status.WorktreeExists = false
		status.Category = StaleCategoryBroken
		status.Reasons = append(status.Reasons, "worktree missing")
		return status
	} else {
		status.GitStatusUnknown = true
		status.Reasons = append(status.Reasons, "worktree inaccessible")
	}

	if opts.includeGit {
		inspectGitState(sess.Path, &status, opts)
	} else {
		status.GitChecksIncomplete = true
	}
	return status
}

func classifyStaleStatus(status StaleStatus, sess *Session, opts StaleAnalysisOptions, threshold time.Duration) StaleStatus {
	if sess == nil {
		status.Category = StaleCategoryBroken
		return status
	}
	if status.Category == StaleCategoryBroken {
		return status
	}
	if !sess.LastReviewedAt.IsZero() && time.Since(sess.LastReviewedAt) < threshold {
		status.Reasons = append(status.Reasons, "reviewed recently")
		return status
	}
	if status.Age < threshold {
		status.Reasons = append(status.Reasons, "recently active")
		return status
	}
	status.Reasons = append(status.Reasons, fmt.Sprintf("inactive for %s", formatAge(status.Age)))

	if status.TmuxStatus == "attached" {
		status.Reasons = append(status.Reasons, "tmux attached")
	} else if status.TmuxStatus == "detached" {
		status.Reasons = append(status.Reasons, "tmux session exists")
	}
	if status.EditorStatus == "running" {
		status.Reasons = append(status.Reasons, "editor running")
	}

	blocked := status.TmuxStatus != "none" || status.EditorStatus == "running" || status.HasUncommitted || status.HasUntracked || status.HasUnpushedCommits
	if status.GitStatusUnknown && opts.unknownGitBlocks {
		blocked = true
	}
	if status.UnpushedStatusUnknown && opts.unknownUnpushedBlocks {
		blocked = true
	}
	if status.HasIgnored && opts.ignoredBlocksCleanup {
		blocked = true
	}

	if blocked {
		status.Category = StaleCategoryNeedsReview
	} else {
		status.Category = StaleCategoryClean
		status.CleanupCandidate = opts.includeGit
		status.PotentialCleanupCandidate = !opts.includeGit
		if opts.includeGit {
			status.Reasons = append(status.Reasons, "clean worktree")
		} else {
			status.Reasons = append(status.Reasons, "stopped and old enough")
		}
	}
	return status
}

func DeriveSessionStatus(sess *Session, stale StaleStatus, artifactCount, unseenArtifacts int) SessionStatusSummary {
	if sess == nil {
		sess = &Session{}
	}
	status := SessionStatusSummary{
		Primary:          SessionStatusIdle,
		Color:            "gray",
		Label:            "idle",
		Priority:         70,
		Reasons:          append([]string(nil), stale.Reasons...),
		Dirty:            stale.HasUncommitted || stale.HasUntracked || stale.HasUnpushedCommits,
		UnseenArtifacts:  unseenArtifacts,
		ArtifactCount:    artifactCount,
		WorktreeExists:   stale.WorktreeExists,
		TmuxStatus:       stale.TmuxStatus,
		EditorRunning:    stale.EditorStatus == "running",
		CleanupCandidate: stale.CleanupCandidate,
		ChecksComplete:   !stale.GitChecksIncomplete,
		CleanupVerified:  stale.CleanupCandidate,
		CheckedAt:        time.Now(),
	}
	if sess.AttentionFlag {
		status.Primary, status.Color, status.Label, status.Priority = SessionStatusAttention, "orange", "attention", 10
		status.Badges = append(status.Badges, "!")
		if sess.AttentionReason != "" {
			status.Reasons = append([]string{sess.AttentionReason}, status.Reasons...)
		}
		return status
	}
	if unseenArtifacts > 0 {
		status.Primary, status.Color, status.Label, status.Priority = SessionStatusUnseenArtifact, "cyan", "new artifact", 20
		status.Badges = append(status.Badges, "new ◆")
		return status
	}
	if stale.Category == StaleCategoryBroken || stale.GitStatusUnknown {
		status.Primary, status.Color, status.Label, status.Priority = SessionStatusBrokenOrStale, "red", "needs repair", 30
		status.Badges = append(status.Badges, "⚠")
		return status
	}
	if status.Dirty {
		status.Primary, status.Color, status.Label, status.Priority = SessionStatusDirty, "yellow", "dirty", 40
		status.Badges = append(status.Badges, "±")
		return status
	}
	if stale.Category == StaleCategoryActive || stale.TmuxStatus != "none" || stale.EditorStatus == "running" {
		status.Primary, status.Color, status.Label, status.Priority = SessionStatusActive, "green", "active", 50
		status.Badges = append(status.Badges, "▶")
		return status
	}
	if stale.Category == StaleCategoryNeedsReview {
		status.Primary, status.Color, status.Label, status.Priority = SessionStatusBrokenOrStale, "orange", "needs review", 55
		status.Badges = append(status.Badges, "?")
		return status
	}
	if stale.CleanupCandidate {
		status.Primary, status.Color, status.Label, status.Priority = SessionStatusCleanupCandidate, "gray", "cleanup candidate", 60
		status.Badges = append(status.Badges, "🧹")
		return status
	}
	if stale.PotentialCleanupCandidate {
		status.Primary, status.Color, status.Label, status.Priority = SessionStatusIdle, "gray", "stale idle (not verified)", 65
		status.Badges = append(status.Badges, "scan")
		return status
	}
	return status
}

func StaleThresholdDuration(days int) (time.Duration, error) {
	if days <= 0 {
		return 0, fmt.Errorf("days must be a positive integer")
	}
	if days > MaxStaleThresholdDays {
		return 0, fmt.Errorf("days must be <= %d", MaxStaleThresholdDays)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

func normalizeThreshold(threshold time.Duration) time.Duration {
	if threshold <= 0 {
		return DefaultStaleThreshold
	}
	max := time.Duration(MaxStaleThresholdDays) * 24 * time.Hour
	if threshold > max {
		return max
	}
	return threshold
}

func sessionLastActiveAt(sess *Session) time.Time {
	if sess == nil {
		return time.Now()
	}
	last := sess.CreatedAt
	for _, candidate := range []time.Time{sess.UpdatedAt, sess.LastAttached} {
		if candidate.After(last) {
			last = candidate
		}
	}
	if last.After(time.Time{}) {
		return last
	}
	return time.Now()
}

func inspectGitState(path string, status *StaleStatus, opts StaleAnalysisOptions) {
	args := []string{"status", "--porcelain"}
	if opts.includeIgnored {
		args = append(args, "--ignored")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	output, err := cmd.Output()
	if err != nil {
		status.GitStatusUnknown = true
		status.Reasons = append(status.Reasons, "git status unavailable")
		return
	}
	for _, line := range strings.Split(strings.TrimRight(string(output), "\n"), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "??") {
			status.HasUntracked = true
		} else if strings.HasPrefix(line, "!!") {
			status.HasIgnored = true
		} else {
			status.HasUncommitted = true
		}
	}
	if status.HasUncommitted {
		status.Reasons = append(status.Reasons, "modified or staged files")
	}
	if status.HasUntracked {
		status.Reasons = append(status.Reasons, "untracked files")
	}
	if status.HasIgnored {
		status.Reasons = append(status.Reasons, "ignored files")
	}

	if !opts.includeUnpushed {
		return
	}
	count, ok := unpushedCommitCount(path)
	if !ok {
		status.UnpushedStatusUnknown = true
		status.Reasons = append(status.Reasons, "unpushed commit status unavailable")
		return
	}
	if count > 0 {
		status.HasUnpushedCommits = true
		status.UnpushedCommits = count
		status.Reasons = append(status.Reasons, fmt.Sprintf("%d unpushed commit(s)", count))
	}
}

func unpushedCommitCount(path string) (count int, ok bool) {
	upstream := "@{upstream}"
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", upstream)
	cmd.Dir = path
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "no upstream") || strings.Contains(stderr.String(), "no such branch") {
			base, baseOK := fallbackBaseRef(path)
			if !baseOK {
				return 0, false
			}
			upstream = base
		} else {
			return 0, false
		}
	}

	cmd = exec.Command("git", "rev-list", "--count", upstream+"..HEAD")
	cmd.Dir = path
	output, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	count, err = strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, false
	}
	return count, true
}

func fallbackBaseRef(path string) (string, bool) {
	// Only remote-tracking refs are safe as an unpushed-commit baseline. Local
	// main/master may itself contain unpushed work, in which case main..HEAD can
	// incorrectly report zero commits for the checked-out branch.
	for _, candidate := range []string{"origin/main", "origin/master"} {
		cmd := exec.Command("git", "rev-parse", "--verify", candidate)
		cmd.Dir = path
		if cmd.Run() == nil {
			return candidate, true
		}
	}
	return "", false
}

func tmuxStatus(sessionName string) string {
	if statuses := tmuxSessionStatuses(); len(statuses) > 0 {
		if status, ok := statuses[sessionName]; ok {
			return status
		}
	}
	return "none"
}

func tmuxSessionStatuses() map[string]string {
	statuses := make(map[string]string)
	if _, err := exec.LookPath("tmux"); err != nil {
		return statuses
	}
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}\t#{session_attached}")
	output, err := cmd.Output()
	if err != nil {
		return statuses
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		name, attached, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if strings.TrimSpace(attached) == "1" {
			statuses[name] = "attached"
		} else {
			statuses[name] = "detached"
		}
	}
	return statuses
}

func formatAge(d time.Duration) string {
	if d < 24*time.Hour {
		return d.Round(time.Hour).String()
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
