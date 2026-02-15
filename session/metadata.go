package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jfox85/devx/config"
)

type Session struct {
	Name            string            `json:"name"`
	ProjectAlias    string            `json:"project_alias,omitempty"` // Reference to project in registry
	ProjectPath     string            `json:"project_path,omitempty"`  // Resolved project path
	Branch          string            `json:"branch"`
	Path            string            `json:"path"`
	Ports           map[string]int    `json:"ports"`
	Routes          map[string]string `json:"routes,omitempty"`     // service -> hostname mapping
	EditorPID       int               `json:"editor_pid,omitempty"` // PID of the editor process
	AttentionFlag   bool              `json:"attention_flag,omitempty"`
	AttentionReason string            `json:"attention_reason,omitempty"` // "claude_done", "claude_stuck", "manual", etc.
	AttentionTime   time.Time         `json:"attention_time,omitempty"`
	LastAttached    time.Time         `json:"last_attached,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type SessionStore struct {
	Sessions      map[string]*Session `json:"sessions"`
	NumberedSlots map[int]string      `json:"numbered_slots,omitempty"`
}

// LoadSessions loads the sessions from the metadata file
func LoadSessions() (*SessionStore, error) {
	sessionsPath := getSessionsPath()

	// Create config directory if it doesn't exist
	dir := filepath.Dir(sessionsPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	// If file doesn't exist, return empty store
	if _, err := os.Stat(sessionsPath); os.IsNotExist(err) {
		return &SessionStore{
			Sessions:      make(map[string]*Session),
			NumberedSlots: make(map[int]string),
		}, nil
	}

	data, err := os.ReadFile(sessionsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions file: %w", err)
	}

	var store SessionStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("failed to parse sessions file: %w", err)
	}

	if store.Sessions == nil {
		store.Sessions = make(map[string]*Session)
	}
	if store.NumberedSlots == nil {
		store.NumberedSlots = make(map[int]string)
	}

	return &store, nil
}

// SaveSessions saves the sessions to the metadata file
func (s *SessionStore) Save() error {
	sessionsPath := getSessionsPath()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sessions: %w", err)
	}

	if err := os.WriteFile(sessionsPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write sessions file: %w", err)
	}

	return nil
}

// AddSession adds a new session to the store
func (s *SessionStore) AddSession(name, branch, path string, ports map[string]int) error {
	if _, exists := s.Sessions[name]; exists {
		return fmt.Errorf("session %s already exists", name)
	}

	now := time.Now()
	s.Sessions[name] = &Session{
		Name:      name,
		Branch:    branch,
		Path:      path,
		Ports:     ports,
		CreatedAt: now,
		UpdatedAt: now,
	}

	return s.Save()
}

// AddSessionWithProject adds a new session to the store with project information
func (s *SessionStore) AddSessionWithProject(name, branch, path string, ports map[string]int, projectAlias, projectPath string) error {
	if _, exists := s.Sessions[name]; exists {
		return fmt.Errorf("session %s already exists", name)
	}

	now := time.Now()
	s.Sessions[name] = &Session{
		Name:         name,
		ProjectAlias: projectAlias,
		ProjectPath:  projectPath,
		Branch:       branch,
		Path:         path,
		Ports:        ports,
		Routes:       make(map[string]string),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	return s.Save()
}

// GetSession retrieves a session by name
func (s *SessionStore) GetSession(name string) (*Session, bool) {
	session, exists := s.Sessions[name]
	return session, exists
}

// UpdateSession updates an existing session
func (s *SessionStore) UpdateSession(name string, updateFn func(*Session)) error {
	session, exists := s.Sessions[name]
	if !exists {
		return fmt.Errorf("session %s not found", name)
	}

	updateFn(session)
	session.UpdatedAt = time.Now()

	return s.Save()
}

// RecordAttach updates the LastAttached timestamp for a session
func (s *SessionStore) RecordAttach(name string) error {
	return s.UpdateSession(name, func(sess *Session) {
		sess.LastAttached = time.Now()
	})
}

// RemoveSession removes a session from the store
func (s *SessionStore) RemoveSession(name string) error {
	if _, exists := s.Sessions[name]; !exists {
		return fmt.Errorf("session %s not found", name)
	}

	delete(s.Sessions, name)
	return s.Save()
}

// LoadRegistry is an alias for LoadSessions for compatibility
func LoadRegistry() (*SessionStore, error) {
	return LoadSessions()
}

// ClearRegistry removes all sessions and clears the sessions file
func ClearRegistry() error {
	store := &SessionStore{
		Sessions:      make(map[string]*Session),
		NumberedSlots: make(map[int]string),
	}
	return store.Save()
}

// RemoveSession removes a single session completely (helper for commands)
func RemoveSession(name string, sess *Session) error {
	// Terminate editor if it's running
	_ = TerminateEditor(name) // Don't fail on editor termination errors

	// Kill tmux session if it exists
	_ = killTmuxSession(name) // Don't fail on tmux errors

	// Remove git worktree
	_ = removeGitWorktree(sess.Path) // Don't fail on worktree errors

	return nil
}

func killTmuxSession(sessionName string) error {
	// Check if tmux is available
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil // tmux not available, skip
	}

	// Try to kill the session
	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	err := cmd.Run()

	// Don't treat "session not found" as an error
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil // session doesn't exist, which is fine
		}
		return err
	}

	fmt.Printf("Killed tmux session '%s'\n", sessionName)
	return nil
}

func removeGitWorktree(worktreePath string) error {
	// Check if worktree exists
	if _, err := os.Stat(worktreePath); os.IsNotExist(err) {
		return nil // already removed
	}

	// Use git worktree remove command with --force flag
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If git command fails, try manual removal
		if removeErr := os.RemoveAll(worktreePath); removeErr != nil {
			return fmt.Errorf("failed to remove worktree: git error: %v; manual removal error: %v",
				string(output), removeErr)
		}
		fmt.Printf("Manually removed worktree directory\n")
	} else {
		fmt.Printf("Removed git worktree\n")
	}

	return nil
}

func getSessionsPath() string {
	return config.GetSessionsPath()
}

// SetAttentionFlag sets the attention flag for a session
func SetAttentionFlag(sessionName, reason string) error {
	store, err := LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	_, exists := store.GetSession(sessionName)
	if !exists {
		return fmt.Errorf("session '%s' not found", sessionName)
	}

	return store.UpdateSession(sessionName, func(s *Session) {
		s.AttentionFlag = true
		s.AttentionReason = reason
		s.AttentionTime = time.Now()
	})
}

// ClearAttentionFlag clears the attention flag for a session
func ClearAttentionFlag(sessionName string) error {
	store, err := LoadSessions()
	if err != nil {
		return fmt.Errorf("failed to load sessions: %w", err)
	}

	_, exists := store.GetSession(sessionName)
	if !exists {
		return fmt.Errorf("session '%s' not found", sessionName)
	}

	return store.UpdateSession(sessionName, func(s *Session) {
		s.AttentionFlag = false
		s.AttentionReason = ""
		s.AttentionTime = time.Time{}
	})
}

// GetCurrentSessionName attempts to determine the current session based on working directory
func GetCurrentSessionName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	// Load sessions to check if current directory matches any session path
	store, err := LoadSessions()
	if err != nil {
		return ""
	}

	for name, sess := range store.Sessions {
		if sess.Path == cwd {
			return name
		}
	}

	// Check if we're in a worktree subdirectory of any session
	for name, sess := range store.Sessions {
		if strings.HasPrefix(cwd, sess.Path+"/") {
			return name
		}
	}

	// Check tmux session name if we're in tmux
	if tmuxSession := os.Getenv("TMUX"); tmuxSession != "" {
		// Try to get tmux session name
		cmd := exec.Command("tmux", "display-message", "-p", "#{session_name}")
		output, err := cmd.Output()
		if err == nil {
			sessionName := strings.TrimSpace(string(output))
			// Check if this tmux session name matches any of our devx sessions
			if _, exists := store.Sessions[sessionName]; exists {
				return sessionName
			}
		}
	}

	return ""
}

// AssignSlot assigns a numbered slot (1-9) to a session.
// If the session already has a slot, returns the existing slot (stable).
// If a free slot exists, assigns the lowest available.
// If all 9 are full, evicts the session with the oldest LastAttached.
func (s *SessionStore) AssignSlot(name string) (int, error) {
	if _, exists := s.Sessions[name]; !exists {
		return 0, fmt.Errorf("session '%s' not found", name)
	}

	// Check if session already has a slot
	if slot := s.GetSlotForSession(name); slot != 0 {
		return slot, nil
	}

	// Find lowest available slot (1-9)
	for i := 1; i <= 9; i++ {
		if _, taken := s.NumberedSlots[i]; !taken {
			s.NumberedSlots[i] = name
			return i, s.Save()
		}
	}

	// All slots full — evict the session with the oldest LastAttached.
	// Iterate slots in ascending order for deterministic tie-breaking.
	slots := make([]int, 0, len(s.NumberedSlots))
	for slot := range s.NumberedSlots {
		slots = append(slots, slot)
	}
	sort.Ints(slots)

	oldestSlot := 0
	var oldestTime time.Time
	for _, slot := range slots {
		sessName := s.NumberedSlots[slot]
		sess, exists := s.Sessions[sessName]
		if !exists {
			// Stale slot, use it immediately
			s.NumberedSlots[slot] = name
			return slot, s.Save()
		}
		if oldestSlot == 0 || sess.LastAttached.Before(oldestTime) {
			oldestSlot = slot
			oldestTime = sess.LastAttached
		}
	}

	s.NumberedSlots[oldestSlot] = name
	return oldestSlot, s.Save()
}

// GetSlotForSession returns the slot number for a session, or 0 if unassigned.
func (s *SessionStore) GetSlotForSession(name string) int {
	for slot, sessName := range s.NumberedSlots {
		if sessName == name {
			return slot
		}
	}
	return 0
}

// GetSessionForSlot returns the session name assigned to a slot, or "" if empty.
func (s *SessionStore) GetSessionForSlot(slot int) string {
	return s.NumberedSlots[slot]
}

// ReconcileSlots removes slot assignments for sessions that no longer exist.
func (s *SessionStore) ReconcileSlots() {
	for slot, name := range s.NumberedSlots {
		if _, exists := s.Sessions[name]; !exists {
			delete(s.NumberedSlots, slot)
		}
	}
}
