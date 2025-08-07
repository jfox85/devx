package session

import (
	"bufio"
	"bytes"
	"fmt"
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

	// Check if already exists
	exists, existingPath, err := IsWorktreeCheckedOut(repoPath, name)
	if err != nil {
		return err
	}

	if exists && !detach {
		return fmt.Errorf("session already exists at %s", existingPath)
	}

	// Check if branch exists
	branchExists, err := BranchExists(repoPath, name)
	if err != nil {
		return err
	}

	var cmd *exec.Cmd
	if branchExists {
		// Branch exists, just add worktree
		cmd = exec.Command("git", "worktree", "add", worktreePath, name)
	} else {
		// Create new branch with worktree
		cmd = exec.Command("git", "worktree", "add", "-b", name, worktreePath)
	}

	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create worktree: %w\n%s", err, output)
	}

	return nil
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
