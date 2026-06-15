package session

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type WorktreeInfo struct {
	Path   string
	Branch string
	Head   string
}

// ListWorktrees returns all git worktrees in the repository
func ListWorktrees(repoPath string) ([]WorktreeInfo, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = repoPath

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list worktrees: %w", err)
	}

	return parseWorktreeOutput(output), nil
}

func parseWorktreeOutput(output []byte) []WorktreeInfo {
	var worktrees []WorktreeInfo
	var current WorktreeInfo

	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			if current.Path != "" {
				worktrees = append(worktrees, current)
				current = WorktreeInfo{}
			}
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}

		switch parts[0] {
		case "worktree":
			current.Path = parts[1]
		case "HEAD":
			current.Head = parts[1]
		case "branch":
			current.Branch = strings.TrimPrefix(parts[1], "refs/heads/")
		}
	}

	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees
}

// IsWorktreeCheckedOut checks if a branch is already checked out in a worktree
func IsWorktreeCheckedOut(repoPath, branchName string) (bool, string, error) {
	worktrees, err := ListWorktrees(repoPath)
	if err != nil {
		return false, "", err
	}

	for _, wt := range worktrees {
		if wt.Branch == branchName {
			return true, wt.Path, nil
		}
	}

	return false, "", nil
}

// CreateWorktree creates a new git worktree and returns the path to it.
// If the branch is already checked out in an existing worktree (anywhere in
// the repo — including paths created by external tools like Claude Code),
// that existing worktree path is returned and reused rather than failing.
func CreateWorktree(repoPath, name string, detach bool) (string, error) {
	worktreePath := filepath.Join(repoPath, ".worktrees", name)

	// Prune stale worktree registrations upfront. This handles the case where a
	// worktree directory was deleted externally (e.g. via `devx session rm`) but
	// git's internal bookkeeping still has an entry for the path, which would
	// cause `git worktree add` to fail with "missing but already registered".
	if err := PruneWorktrees(repoPath); err != nil {
		fmt.Printf("Warning: failed to prune worktrees: %v\n", err)
	}

	// First, check if the worktree directory exists on disk
	if _, err := os.Stat(worktreePath); err == nil {
		// Directory exists, check if it's a valid worktree
		worktreeExists, err := WorktreeExists(repoPath, worktreePath)
		if err != nil {
			return "", err
		}

		if worktreeExists {
			// Worktree exists and is valid
			// Check if it's on the expected branch
			worktrees, err := ListWorktrees(repoPath)
			if err != nil {
				return "", err
			}

			for _, wt := range worktrees {
				if wt.Path == worktreePath {
					if wt.Branch == name {
						// Already on correct branch, we can reuse it
						fmt.Printf("Reusing existing worktree at %s (branch: %s)\n", worktreePath, name)
						return worktreePath, nil
					} else if !detach {
						return "", fmt.Errorf("worktree at %s exists but is on branch %s, not %s. Use --detach to override", worktreePath, wt.Branch, name)
					}
					// If detach is true, we'll remove and recreate below
					break
				}
			}

			if detach {
				// Remove the existing worktree to recreate it
				cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
				cmd.Dir = repoPath
				if output, err := cmd.CombinedOutput(); err != nil {
					return "", fmt.Errorf("failed to remove existing worktree: %w\n%s", err, output)
				}
			}
		} else if !detach {
			// Directory exists but isn't a worktree - this shouldn't happen in normal flow
			return "", fmt.Errorf("directory %s exists but is not a git worktree. Remove it manually or use --detach", worktreePath)
		} else {
			// detach is true and directory exists but isn't a worktree
			// Remove the directory
			if err := os.RemoveAll(worktreePath); err != nil {
				return "", fmt.Errorf("failed to remove existing directory: %w", err)
			}
		}
	}

	// Fetch from origin to ensure remote refs are current (non-fatal)
	if err := FetchOrigin(repoPath); err != nil {
		fmt.Printf("Warning: could not fetch from origin: %v\n", err)
	}

	// Check local branch first
	branchExists, err := BranchExists(repoPath, name)
	if err != nil {
		return "", err
	}

	var (
		cmd             *exec.Cmd
		pullAfterCreate bool
	)
	if branchExists {
		// Local branch exists — check it out, then pull to get any remote commits
		cmd = exec.Command("git", "worktree", "add", worktreePath, name)
		pullAfterCreate = true
	} else {
		// Check for remote branch
		remoteBranchExists, err := RemoteBranchExists(repoPath, name)
		if err != nil {
			return "", err
		}
		if remoteBranchExists {
			// Create local branch from remote, tracking origin.
			// We just fetched, so origin/<name> is already up-to-date.
			fmt.Printf("Found existing remote branch '%s', using it for this session.\n", name)
			cmd = exec.Command("git", "worktree", "add", "-b", name, worktreePath, "origin/"+name)
		} else {
			// No existing branch — create new from HEAD (current behavior)
			cmd = exec.Command("git", "worktree", "add", "-b", name, worktreePath)
		}
	}

	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Both old and new git versions report the branch-in-use error differently:
		//   old: "is already checked out at <path>"
		//   new: "is already used by worktree at <path>"
		// In both cases, find the existing worktree and adopt it for this session.
		outputStr := string(output)
		if strings.Contains(outputStr, "is already checked out") ||
			strings.Contains(outputStr, "is already used by worktree") {
			checkedOut, existingPath, checkErr := IsWorktreeCheckedOut(repoPath, name)
			if checkErr == nil && checkedOut {
				fmt.Printf("Branch '%s' is already checked out at %s, adopting it for this session.\n", name, existingPath)
				return existingPath, nil
			}
		}
		return "", fmt.Errorf("failed to create worktree: %w\n%s", err, output)
	}

	// For existing local branches, pull from remote to pick up any commits we're missing
	if pullAfterCreate {
		if pullErr := PullFromOrigin(worktreePath, name); pullErr != nil {
			fmt.Printf("Warning: could not pull latest changes for '%s': %v\n", name, pullErr)
		}

		// Check whether we're still behind remote after the pull attempt and warn if so
		behindCmd := exec.Command("git", "rev-list", "--count", fmt.Sprintf("HEAD..origin/%s", name))
		behindCmd.Dir = worktreePath
		if out, err := behindCmd.Output(); err == nil {
			if count := strings.TrimSpace(string(out)); count != "0" {
				fmt.Printf("Note: session branch '%s' is %s commit(s) behind origin/%s (branches may have diverged).\n", name, count, name)
				fmt.Printf("      Run 'git rebase origin/%s' in the session to incorporate remote changes.\n", name)
			}
		}
	}

	return worktreePath, nil
}

// FetchOrigin fetches remote refs from origin, pruning deleted branches.
// Failure is non-fatal (caller warns and continues).
func FetchOrigin(repoPath string) error {
	cmd := exec.Command("git", "fetch", "origin", "--prune")
	cmd.Dir = repoPath
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch failed: %w\n%s", err, output)
	}
	return nil
}

// RemoteBranchExists checks if a branch exists on origin.
func RemoteBranchExists(repoPath, branchName string) (bool, error) {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet",
		fmt.Sprintf("refs/remotes/origin/%s", branchName))
	cmd.Dir = repoPath
	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("failed to check remote branch existence: %w", err)
	}
	return true, nil
}

// BranchExists checks if a branch exists in the repository
func BranchExists(repoPath, branchName string) (bool, error) {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s", branchName))
	cmd.Dir = repoPath

	err := cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// Branch doesn't exist
			return false, nil
		}
		return false, fmt.Errorf("failed to check branch existence: %w", err)
	}

	return true, nil
}

// PruneWorktrees removes stale worktree references.
//
// It deliberately uses git's default grace period (it does NOT pass
// --expire now). An aggressive `--expire now` previously turned transient
// worktree inconsistencies — e.g. a containerized session whose .git metadata
// briefly looked "missing" from the host's perspective — into permanent data
// loss by immediately pruning the registration and dropping the branch. The
// default grace period gives such worktrees time to recover before any prune.
func PruneWorktrees(repoPath string) error {
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = repoPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to prune worktrees: %w\n%s", err, output)
	}

	return nil
}

// CreateIndependentClone creates a standalone shallow git clone for a
// containerized (docker/gatepost) session at .worktrees/<name>, instead of a
// linked git worktree. A standalone clone has no shared .git/worktrees/<name>
// metadata and no dependency on the host repo's object store, so it cannot be
// corrupted by container-side git operations or by host-side worktree pruning.
//
// The clone is created from the source repo's default branch (origin/main or
// origin/master) at a shallow depth, then the session branch is created on top.
// Because sessions are almost always branched off current main, even a shallow
// clone keeps `origin/main...HEAD` diffs/reviews working: HEAD starts at the
// fetched tip, and all session commits descend from it.
//
// If the worktree directory already exists and is a valid git repo, it is
// reused as-is (idempotent restarts).
func CreateIndependentClone(repoPath, name string) (string, error) {
	worktreePath := filepath.Join(repoPath, ".worktrees", name)

	// Reuse an existing valid clone (idempotent restart).
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err == nil {
		if exec.Command("git", "-C", worktreePath, "rev-parse", "--git-dir").Run() == nil {
			fmt.Printf("Reusing existing session repo at %s\n", worktreePath)
			return worktreePath, nil
		}
	}

	// Refuse to clobber a non-empty, non-repo directory to avoid destroying data.
	if entries, err := os.ReadDir(worktreePath); err == nil && len(entries) > 0 {
		return "", fmt.Errorf("directory %s exists but is not a git repository; remove it manually", worktreePath)
	}

	defaultBranch, err := defaultRemoteBranch(repoPath)
	if err != nil {
		return "", err
	}

	origin, err := remoteURL(repoPath)
	if err != nil {
		return "", err
	}

	// Determine up-front whether the session branch already exists on origin,
	// using the source repo's remote refs (the shallow single-branch clone below
	// will not contain other branches' refs).
	sessionBranchOnRemote, err := RemoteBranchExists(repoPath, name)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return "", fmt.Errorf("failed to create worktrees directory: %w", err)
	}

	// Shallow, single-branch clone of the default branch. Depth gives a little
	// history headroom for tooling (blame, short logs) without paying for full
	// history; merge-base with the default branch stays reachable for reviews.
	cloneCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	clone := exec.CommandContext(cloneCtx, "git", "clone",
		"--depth", "20",
		"--single-branch",
		"--branch", defaultBranch,
		origin, worktreePath)
	if output, err := clone.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to clone session repo: %w\n%s", err, output)
	}

	// If a branch named after the session already exists on origin, fetch and
	// check it out; otherwise create a fresh branch off the default branch tip.
	if sessionBranchOnRemote {
		fetch := exec.Command("git", "-C", worktreePath, "fetch", "--depth", "20", "origin", name)
		if output, err := fetch.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to fetch existing branch %q: %w\n%s", name, err, output)
		}
		co := exec.Command("git", "-C", worktreePath, "checkout", "-b", name, "FETCH_HEAD")
		if output, err := co.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to check out branch %q: %w\n%s", name, err, output)
		}
	} else {
		co := exec.Command("git", "-C", worktreePath, "checkout", "-b", name)
		if output, err := co.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to create session branch %q: %w\n%s", name, err, output)
		}
	}

	return worktreePath, nil
}

// defaultRemoteBranch resolves the source repo's default branch, preferring
// origin/main then origin/master, falling back to the checked-out branch.
func defaultRemoteBranch(repoPath string) (string, error) {
	for _, b := range []string{"main", "master"} {
		check := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet",
			"refs/remotes/origin/"+b)
		if check.Run() == nil {
			return b, nil
		}
	}
	// Fall back to the source repo's current branch.
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("could not determine default branch for %s: %w", repoPath, err)
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" || branch == "HEAD" {
		return "", fmt.Errorf("could not determine default branch for %s", repoPath)
	}
	return branch, nil
}

// remoteURL returns the origin URL of the source repo so the clone points at the
// real remote (for fetch/push/PR), not at the local source path.
func remoteURL(repoPath string) (string, error) {
	out, err := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("could not read origin URL for %s: %w", repoPath, err)
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return "", fmt.Errorf("origin URL for %s is empty", repoPath)
	}
	return url, nil
}

// WorktreeExists checks if a worktree exists at the specified path
func WorktreeExists(repoPath, worktreePath string) (bool, error) {
	worktrees, err := ListWorktrees(repoPath)
	if err != nil {
		return false, err
	}

	for _, wt := range worktrees {
		if wt.Path == worktreePath {
			return true, nil
		}
	}

	return false, nil
}

// PullFromOrigin pulls the latest changes from origin for the specified branch
func PullFromOrigin(repoPath, branch string) error {
	// First, check if we have uncommitted changes
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = repoPath
	output, err := statusCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check git status: %w", err)
	}

	// If there are uncommitted changes, we should not pull
	if len(strings.TrimSpace(string(output))) > 0 {
		return fmt.Errorf("cannot pull: repository has uncommitted changes")
	}

	// Fetch from origin first — with a timeout so a slow/unreachable remote
	// doesn't block session creation indefinitely.
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer fetchCancel()
	fetchCmd := exec.CommandContext(fetchCtx, "git", "fetch", "origin")
	fetchCmd.Dir = repoPath
	if output, err := fetchCmd.CombinedOutput(); err != nil {
		// Check if it's a network error or missing remote
		if strings.Contains(string(output), "Could not resolve host") ||
			strings.Contains(string(output), "unable to access") ||
			strings.Contains(string(output), "does not appear to be a git repository") {
			// Network or remote issue - return nil to continue without pulling
			return nil
		}
		return fmt.Errorf("failed to fetch from origin: %w\n%s", err, output)
	}

	// Now pull from origin
	pullCtx, pullCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer pullCancel()
	pullCmd := exec.CommandContext(pullCtx, "git", "pull", "origin", branch, "--ff-only")
	pullCmd.Dir = repoPath
	if output, err := pullCmd.CombinedOutput(); err != nil {
		// Check for common non-fatal errors
		outputStr := string(output)
		if strings.Contains(outputStr, "Couldn't find remote ref") {
			// Branch doesn't exist on remote yet - that's OK
			return nil
		}
		if strings.Contains(outputStr, "not a valid object name") {
			// Branch doesn't exist on remote - that's OK
			return nil
		}
		if strings.Contains(outputStr, "Would overwrite") ||
			strings.Contains(outputStr, "diverged") ||
			strings.Contains(outputStr, "Not possible to fast-forward") {
			// Diverged branches or merge conflict - skip pull but don't fail.
			// Caller is responsible for informing the user.
			return nil
		}
		return fmt.Errorf("failed to pull from origin/%s: %w\n%s", branch, err, output)
	}

	return nil
}
