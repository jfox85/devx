package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jfox85/devx/session"
)

// Test helper functions for session lifecycle testing

// createTestSession creates a session for testing purposes
func createTestSession(t *testing.T, sessionName string) {
	t.Helper()

	// Create a temporary git repository for testing
	tempDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Create initial commit
	testFile := filepath.Join(tempDir, "README.md")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add: %v", err)
	}

	cmd = exec.Command("git", "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git commit: %v", err)
	}

	// Create branch for session
	cmd = exec.Command("git", "branch", sessionName)
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create branch: %v", err)
	}

	// Change to the temp directory
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}

	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Failed to change directory: %v", err)
	}

	// Cleanup function to restore directory
	t.Cleanup(func() {
		os.Chdir(originalDir)
	})

	// Create the session using our existing functionality
	if err := session.CreateWorktree(tempDir, sessionName, false); err != nil {
		t.Fatalf("Failed to create worktree: %v", err)
	}

	// Load sessions store and add metadata
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatalf("Failed to load sessions: %v", err)
	}

	// Allocate ports
	ports := map[string]int{
		"FE_PORT":  8080,
		"API_PORT": 8081,
	}

	worktreePath := filepath.Join(tempDir, ".worktrees", sessionName)
	if err := store.AddSession(sessionName, sessionName, worktreePath, ports); err != nil {
		t.Fatalf("Failed to add session metadata: %v", err)
	}
}

// removeTestSession removes a session for testing purposes
func removeTestSession(t *testing.T, sessionName string) {
	t.Helper()

	// Use our existing session rm functionality
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatalf("Failed to load sessions: %v", err)
	}

	sess, exists := store.GetSession(sessionName)
	if !exists {
		return // Session doesn't exist, nothing to remove
	}

	// Terminate editor if running
	if err := session.TerminateEditor(sessionName); err != nil {
		t.Logf("Warning: failed to terminate editor: %v", err)
	}

	// Kill tmux session if it exists
	if err := killTmuxSession(sessionName); err != nil {
		t.Logf("Warning: failed to kill tmux session: %v", err)
	}

	// Remove git worktree
	if err := removeGitWorktree(sess.Path); err != nil {
		t.Logf("Warning: failed to remove git worktree: %v", err)
	}

	// Remove session from metadata
	if err := store.RemoveSession(sessionName); err != nil {
		t.Fatalf("Failed to remove session metadata: %v", err)
	}
}

// TestSessionRemove tests the complete session removal functionality
func TestSessionRemove(t *testing.T) {
	sessionName := "zap"

	// Create session
	createTestSession(t, sessionName)

	// Verify session exists
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatalf("Failed to load sessions: %v", err)
	}

	sess, exists := store.GetSession(sessionName)
	if !exists {
		t.Fatalf("Session %s was not created", sessionName)
	}

	// Verify worktree exists
	if _, err := os.Stat(sess.Path); os.IsNotExist(err) {
		t.Fatalf("Worktree directory %s does not exist", sess.Path)
	}

	// Remove session
	removeTestSession(t, sessionName)

	// Verify session is removed from metadata
	store, err = session.LoadSessions()
	if err != nil {
		t.Fatalf("Failed to load sessions after removal: %v", err)
	}

	_, exists = store.GetSession(sessionName)
	if exists {
		t.Errorf("Session %s still exists in metadata after removal", sessionName)
	}

	// Verify worktree directory is removed (as specified in requirements)
	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Errorf("Worktree directory %s still exists after removal", sess.Path)
	}
}

func TestSessionListEmpty(t *testing.T) {
	// Clear all sessions first
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatalf("Failed to load sessions: %v", err)
	}

	// Save original sessions and restore after test
	originalSessions := store.Sessions
	store.Sessions = make(map[string]*session.Session)
	if err := store.Save(); err != nil {
		t.Fatalf("Failed to save empty sessions: %v", err)
	}

	defer func() {
		store.Sessions = originalSessions
		_ = store.Save()
	}()

	// Test empty session list - should not panic or error
	err = runSessionList(nil, []string{})
	if err != nil {
		t.Errorf("Session list failed with empty sessions: %v", err)
	}
}

func TestSessionListWithSessions(t *testing.T) {
	sessionName := "test-list-session"

	// Create a test session
	createTestSession(t, sessionName)
	defer removeTestSession(t, sessionName)

	// Test session list with sessions - should not error
	err := runSessionList(nil, []string{})
	if err != nil {
		t.Errorf("Session list failed with sessions: %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	sessionName := "test-lifecycle"

	// Test complete lifecycle: create -> list -> remove

	// 1. Create session
	createTestSession(t, sessionName)

	// 2. Verify it appears in list
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatalf("Failed to load sessions: %v", err)
	}

	_, exists := store.GetSession(sessionName)
	if !exists {
		t.Fatalf("Session %s not found after creation", sessionName)
	}

	// 3. List sessions (should not error)
	err = runSessionList(nil, []string{})
	if err != nil {
		t.Errorf("Session list failed: %v", err)
	}

	// 4. Remove session
	removeTestSession(t, sessionName)

	// 5. Verify it's gone from list
	store, err = session.LoadSessions()
	if err != nil {
		t.Fatalf("Failed to load sessions after removal: %v", err)
	}

	_, exists = store.GetSession(sessionName)
	if exists {
		t.Errorf("Session %s still exists after removal", sessionName)
	}
}
