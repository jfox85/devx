package session

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// CreateWorktree creates a new git worktree
func CreateWorktree(repoPath, name string, detach bool) error {
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
			return err
		}

		if worktreeExists {
			// Worktree exists and is valid
			// Check if it's on the expected branch
			worktrees, err := ListWorktrees(repoPath)
			if err != nil {
				return err
			}

			for _, wt := range worktrees {
				if wt.Path == worktreePath {
					if wt.Branch == name {
						// Already on correct branch, we can reuse it
						fmt.Printf("Reusing existing worktree at %s (branch: %s)\n", worktreePath, name)
						return nil
					} else if !detach {
						return fmt.Errorf("worktree at %s exists but is on branch %s, not %s. Use --detach to override", worktreePath, wt.Branch, name)
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
					return fmt.Errorf("failed to remove existing worktree: %w\n%s", err, output)
				}
			}
		} else if !detach {
			// Directory exists but isn't a worktree - this shouldn't happen in normal flow
			return fmt.Errorf("directory %s exists but is not a git worktree. Remove it manually or use --detach", worktreePath)
		} else {
			// detach is true and directory exists but isn't a worktree
			// Remove the directory
			if err := os.RemoveAll(worktreePath); err != nil {
				return fmt.Errorf("failed to remove existing directory: %w", err)
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
		return err
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
			return err
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
		// Check if error is due to branch being already checked out elsewhere
		if strings.Contains(string(output), "is already checked out") {
			// Try to find where it's checked out
			checkedOut, path, checkErr := IsWorktreeCheckedOut(repoPath, name)
			if checkErr == nil && checkedOut {
				return fmt.Errorf("branch '%s' is already checked out at %s", name, path)
			}
		}
		return fmt.Errorf("failed to create worktree: %w\n%s", err, output)
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

	return nil
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
// --expire now bypasses git's default 3-month grace period so recently-deleted
// worktrees are pruned immediately rather than showing as "missing but registered".
func PruneWorktrees(repoPath string) error {
	cmd := exec.Command("git", "worktree", "prune", "--expire", "now")
	cmd.Dir = repoPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to prune worktrees: %w\n%s", err, output)
	}

	return nil
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

	// Fetch from origin first
	fetchCmd := exec.Command("git", "fetch", "origin")
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
	pullCmd := exec.Command("git", "pull", "origin", branch, "--ff-only")
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
