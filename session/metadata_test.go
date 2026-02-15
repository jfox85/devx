package session

import (
	"fmt"
	"os"
	"testing"
	"time"
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

func TestSessionLastAttached(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	_ = store.AddSession("test-sess", "main", "/path", map[string]int{"PORT": 3000})

	// LastAttached should be zero initially
	sess, _ := store.GetSession("test-sess")
	if !sess.LastAttached.IsZero() {
		t.Error("expected LastAttached to be zero initially")
	}

	// Record attach
	err = store.RecordAttach("test-sess")
	if err != nil {
		t.Fatalf("failed to record attach: %v", err)
	}

	sess, _ = store.GetSession("test-sess")
	if sess.LastAttached.IsZero() {
		t.Error("expected LastAttached to be set after RecordAttach")
	}
}

func TestNumberedSlots_AssignSlot(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	_ = store.AddSession("sess-a", "main", "/a", map[string]int{})
	_ = store.AddSession("sess-b", "main", "/b", map[string]int{})

	// Assign slot for sess-a — should get slot 1 (lowest available)
	slot, err := store.AssignSlot("sess-a")
	if err != nil {
		t.Fatalf("failed to assign slot: %v", err)
	}
	if slot != 1 {
		t.Errorf("expected slot 1, got %d", slot)
	}

	// Assign slot for sess-b — should get slot 2
	slot, err = store.AssignSlot("sess-b")
	if err != nil {
		t.Fatalf("failed to assign slot: %v", err)
	}
	if slot != 2 {
		t.Errorf("expected slot 2, got %d", slot)
	}

	// Assign again for sess-a — should keep slot 1 (stable)
	slot, err = store.AssignSlot("sess-a")
	if err != nil {
		t.Fatalf("failed to assign slot: %v", err)
	}
	if slot != 1 {
		t.Errorf("expected sess-a to keep slot 1, got %d", slot)
	}
}

func TestNumberedSlots_EvictLRU(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()

	// Create 10 sessions, assign slots to first 9
	for i := 1; i <= 10; i++ {
		name := fmt.Sprintf("sess-%d", i)
		_ = store.AddSession(name, "main", fmt.Sprintf("/%d", i), map[string]int{})
		// Set LastAttached so sess-1 is oldest
		_ = store.UpdateSession(name, func(s *Session) {
			s.LastAttached = time.Now().Add(time.Duration(i) * time.Minute)
		})
	}

	for i := 1; i <= 9; i++ {
		_, _ = store.AssignSlot(fmt.Sprintf("sess-%d", i))
	}

	// All 9 slots full. Assign slot for sess-10 — should evict sess-1 (oldest LastAttached)
	slot, err := store.AssignSlot("sess-10")
	if err != nil {
		t.Fatalf("failed to assign slot: %v", err)
	}

	// sess-10 should have taken sess-1's slot (slot 1)
	if slot != 1 {
		t.Errorf("expected sess-10 to get slot 1 (evicting sess-1), got %d", slot)
	}

	// sess-1 should no longer have a slot
	if s := store.GetSlotForSession("sess-1"); s != 0 {
		t.Errorf("expected sess-1 to have no slot, got %d", s)
	}
}

func TestNumberedSlots_Reconcile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	_ = store.AddSession("sess-a", "main", "/a", map[string]int{})
	_, _ = store.AssignSlot("sess-a")

	// Remove session, then reconcile — slot should be freed
	_ = store.RemoveSession("sess-a")
	store.ReconcileSlots()

	if s := store.GetSlotForSession("sess-a"); s != 0 {
		t.Errorf("expected no slot for removed session, got %d", s)
	}
}

func TestNumberedSlots_GetSessionForSlot(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "devx-session-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", oldHome)

	store, _ := LoadSessions()
	_ = store.AddSession("sess-a", "main", "/a", map[string]int{})
	_, _ = store.AssignSlot("sess-a")

	name := store.GetSessionForSlot(1)
	if name != "sess-a" {
		t.Errorf("expected 'sess-a' for slot 1, got '%s'", name)
	}

	name = store.GetSessionForSlot(5)
	if name != "" {
		t.Errorf("expected empty for unassigned slot 5, got '%s'", name)
	}
}
