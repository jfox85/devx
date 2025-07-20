package session

import (
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