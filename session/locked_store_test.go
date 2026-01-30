package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLockedStore_ConcurrentUpdates(t *testing.T) {
	// Create temp directory for test
	tmpDir, err := os.MkdirTemp("", "devx-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sessionsPath := filepath.Join(tmpDir, "sessions.json")
	lockPath := filepath.Join(tmpDir, "sessions.json.lock")

	ls := NewLockedSessionStore(sessionsPath, lockPath)

	// Run concurrent updates
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			err := ls.Update(func(store *SessionStore) error {
				// Simulate adding a session
				name := "test-session"
				if store.Sessions == nil {
					store.Sessions = make(map[string]*Session)
				}
				if _, exists := store.Sessions[name]; !exists {
					store.Sessions[name] = &Session{Name: name}
				}
				return nil
			})
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("concurrent update failed: %v", err)
	}

	// Verify final state
	store, err := ls.Load()
	if err != nil {
		t.Fatalf("failed to load final state: %v", err)
	}

	if _, exists := store.Sessions["test-session"]; !exists {
		t.Error("expected test-session to exist")
	}
}
