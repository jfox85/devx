package session

import (
	"os"
	"testing"
)

func TestSessionStore(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Override home directory for test
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	// Test loading empty store
	store, err := LoadSessions()
	if err != nil {
		t.Fatalf("failed to load empty store: %v", err)
	}

	if len(store.Sessions) != 0 {
		t.Errorf("expected empty sessions, got %d", len(store.Sessions))
	}

	// Test adding session
	ports := map[string]int{"FE_PORT": 3000, "API_PORT": 3001}
	err = store.AddSession("test-session", "test-branch", "/path/to/worktree", ports)
	if err != nil {
		t.Fatalf("failed to add session: %v", err)
	}

	// Verify session was added
	session, exists := store.GetSession("test-session")
	if !exists {
		t.Fatal("expected session to exist")
	}

	if session.Name != "test-session" {
		t.Errorf("expected name 'test-session', got %s", session.Name)
	}
	if session.Branch != "test-branch" {
		t.Errorf("expected branch 'test-branch', got %s", session.Branch)
	}
	if session.Path != "/path/to/worktree" {
		t.Errorf("expected path '/path/to/worktree', got %s", session.Path)
	}
	if session.Ports["FE_PORT"] != 3000 {
		t.Errorf("expected FE port 3000, got %d", session.Ports["FE_PORT"])
	}
	if session.Ports["API_PORT"] != 3001 {
		t.Errorf("expected API port 3001, got %d", session.Ports["API_PORT"])
	}

	// Test loading persisted store
	store2, err := LoadSessions()
	if err != nil {
		t.Fatalf("failed to reload store: %v", err)
	}

	if len(store2.Sessions) != 1 {
		t.Errorf("expected 1 session after reload, got %d", len(store2.Sessions))
	}

	// Test adding duplicate session
	duplicatePorts := map[string]int{"OTHER_PORT": 4000}
	err = store2.AddSession("test-session", "other-branch", "/other/path", duplicatePorts)
	if err == nil {
		t.Error("expected error when adding duplicate session")
	}

	// Test removing session
	err = store2.RemoveSession("test-session")
	if err != nil {
		t.Fatalf("failed to remove session: %v", err)
	}

	if len(store2.Sessions) != 0 {
		t.Errorf("expected 0 sessions after removal, got %d", len(store2.Sessions))
	}
}

func TestSessionStoreUpdate(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Override home directory for test
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	updatePorts := map[string]int{"UPDATE_PORT": 5000}
	_ = store.AddSession("update-test", "branch", "/path", updatePorts)

	// Update session
	err = store.UpdateSession("update-test", func(s *Session) {
		s.Path = "/new/path"
	})
	if err != nil {
		t.Fatalf("failed to update session: %v", err)
	}

	session, _ := store.GetSession("update-test")
	if session.Path != "/new/path" {
		t.Errorf("expected updated path '/new/path', got %s", session.Path)
	}

	// Verify UpdatedAt was changed
	if session.UpdatedAt.Equal(session.CreatedAt) {
		t.Error("expected UpdatedAt to be different from CreatedAt")
	}
}
