package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// LockedSessionStore provides thread-safe access to sessions.json
type LockedSessionStore struct {
	sessionsPath string
	lockPath     string
	fileLock     *flock.Flock
}

// NewLockedSessionStore creates a new locked session store
func NewLockedSessionStore(sessionsPath, lockPath string) *LockedSessionStore {
	return &LockedSessionStore{
		sessionsPath: sessionsPath,
		lockPath:     lockPath,
		fileLock:     flock.New(lockPath),
	}
}

// NewDefaultLockedSessionStore creates a locked store with default paths
func NewDefaultLockedSessionStore() *LockedSessionStore {
	sessionsPath := getSessionsPath()
	lockPath := sessionsPath + ".lock"
	return NewLockedSessionStore(sessionsPath, lockPath)
}

// Load loads the sessions with a shared (read) lock
func (ls *LockedSessionStore) Load() (*SessionStore, error) {
	locked, err := ls.fileLock.TryRLock()
	if err != nil {
		return nil, fmt.Errorf("failed to acquire read lock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("could not acquire read lock")
	}
	defer ls.fileLock.Unlock()

	return ls.loadUnlocked()
}

// Update performs an atomic read-modify-write with exclusive lock
func (ls *LockedSessionStore) Update(fn func(*SessionStore) error) error {
	locked, err := ls.fileLock.TryLock()
	if err != nil {
		return fmt.Errorf("failed to acquire write lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("could not acquire write lock")
	}
	defer ls.fileLock.Unlock()

	store, err := ls.loadUnlocked()
	if err != nil {
		return err
	}

	if err := fn(store); err != nil {
		return err
	}

	return ls.saveUnlocked(store)
}

func (ls *LockedSessionStore) loadUnlocked() (*SessionStore, error) {
	// Create directory if needed
	dir := filepath.Dir(ls.sessionsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Return empty store if file doesn't exist
	if _, err := os.Stat(ls.sessionsPath); os.IsNotExist(err) {
		return &SessionStore{Sessions: make(map[string]*Session)}, nil
	}

	data, err := os.ReadFile(ls.sessionsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions: %w", err)
	}

	var store SessionStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("failed to parse sessions: %w", err)
	}

	if store.Sessions == nil {
		store.Sessions = make(map[string]*Session)
	}

	return &store, nil
}

func (ls *LockedSessionStore) saveUnlocked(store *SessionStore) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sessions: %w", err)
	}

	// Create a unique temp file in the same directory (atomic write)
	dir := filepath.Dir(ls.sessionsPath)
	tmpFile, err := os.CreateTemp(dir, ".sessions.*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Write to temp file
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Rename temp to actual (atomic on POSIX)
	if err := os.Rename(tmpPath, ls.sessionsPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}
