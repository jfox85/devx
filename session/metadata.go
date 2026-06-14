package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jfox85/devx/config"
)

type Session struct {
	Name               string            `json:"name"`
	ProjectAlias       string            `json:"project_alias,omitempty"` // Reference to project in registry
	ProjectPath        string            `json:"project_path,omitempty"`  // Resolved project path
	Branch             string            `json:"branch"`
	Path               string            `json:"path"`
	Ports              map[string]int    `json:"ports"`
	Routes             map[string]string `json:"routes,omitempty"`     // service -> hostname mapping
	EditorPID          int               `json:"editor_pid,omitempty"` // PID of the editor process
	AttentionFlag      bool              `json:"attention_flag,omitempty"`
	AttentionReason    string            `json:"attention_reason,omitempty"` // "claude_done", "claude_stuck", "manual", etc.
	AttentionSource    string            `json:"attention_source,omitempty"`
	AttentionTime      time.Time         `json:"attention_time,omitempty"`
	DisplayName        string            `json:"display_name,omitempty"`
	Color              string            `json:"color,omitempty"`
	LastAttached       time.Time         `json:"last_attached,omitempty"`
	LastArtifactSeenAt time.Time         `json:"last_artifact_seen_at,omitempty"`
	LastReviewedAt     time.Time         `json:"last_reviewed_at,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	Target             TargetMeta        `json:"target,omitempty"`
}

// TargetMeta describes the execution environment for a session.
// Zero value (empty Type) is treated as "host" everywhere.
type TargetMeta struct {
	Type          string       `json:"type,omitempty"`           // "host", "docker", future: "gatepost", "vm"
	ContainerID   string       `json:"container_id,omitempty"`   // Docker container ID
	ContainerName string       `json:"container_name,omitempty"` // Docker container name
	NetworkName   string       `json:"network_name,omitempty"`   // Docker network name
	Image         string       `json:"image,omitempty"`          // Image used
	Gatepost      GatepostMeta `json:"gatepost,omitempty"`       // Gatepost runtime metadata when target is gatepost
}

// GatepostMeta describes the optional Gatepost capability attached to a session.
// Runtime-specific details stay behind Runtime; DevX consumes this as a stable
// contract for control, logs, and host-side bypass operations.
type GatepostMeta struct {
	Enabled             bool     `json:"enabled,omitempty"`
	Runtime             string   `json:"runtime,omitempty"`
	ProxyContainerName  string   `json:"proxy_container_name,omitempty"`
	InternalNetworkName string   `json:"internal_network_name,omitempty"`
	EgressNetworkName   string   `json:"egress_network_name,omitempty"`
	PortsNetworkName    string   `json:"ports_network_name,omitempty"`
	SessionDir          string   `json:"session_dir,omitempty"`
	AuditDir            string   `json:"audit_dir,omitempty"`
	ConfigDir           string   `json:"config_dir,omitempty"`
	AuditLog            string   `json:"audit_log,omitempty"`
	CompanionLog        string   `json:"companion_log,omitempty"`
	ControlURL          string   `json:"control_url,omitempty"`
	LogsURL             string   `json:"logs_url,omitempty"`
	LogsTokenPath       string   `json:"logs_token_path,omitempty"`
	LogsPID             int      `json:"logs_pid,omitempty"`
	ProviderMode        string   `json:"provider_mode,omitempty"`
	ProviderCommand     string   `json:"provider_command,omitempty"`
	RegisteredProviders []string `json:"registered_providers,omitempty"`
	ProviderWarnings    []string `json:"provider_warnings,omitempty"`
	ControlToken        string   `json:"-"`
	EventToken          string   `json:"-"`
	Bypass              bool     `json:"bypass,omitempty"`
}

// TargetType returns the effective target type, defaulting to "host".
func (s *Session) TargetType() string {
	if s.Target.Type == "" {
		return "host"
	}
	return s.Target.Type
}

// IsContainerized returns true if the session runs inside a container.
func (s *Session) IsContainerized() bool {
	return s.TargetType() != "host"
}

var ErrSessionNotFound = errors.New("session not found")

var sessionsProcessLock sync.Mutex

type SessionStore struct {
	Sessions      map[string]*Session `json:"sessions"`
	NumberedSlots map[int]string      `json:"numbered_slots,omitempty"`
}

// LoadSessions loads the sessions from the metadata file
func LoadSessions() (*SessionStore, error) {
	return loadSessionsUnlocked()
}

// loadSessionsUnlocked reads and parses the sessions metadata without acquiring
// the sessions lock. Callers that mutate must hold the lock (see withSessionsLock);
// the public LoadSessions is a plain read and intentionally lock-free.
func loadSessionsUnlocked() (*SessionStore, error) {
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

// getSessionsLockPath returns the sidecar advisory lock file path used to
// serialize read-modify-write cycles on the sessions metadata across processes.
func getSessionsLockPath() string {
	return getSessionsPath() + ".lock"
}

// SessionsMetadataFingerprint returns a cheap version string for the sessions
// metadata file. Web caches use this to notice mutations made by other devx
// processes without parsing the whole store on every request.
func SessionsMetadataFingerprint() string {
	info, err := os.Stat(getSessionsPath())
	if err != nil {
		return "missing"
	}
	return fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
}

// withSessionsLock runs fn while holding an exclusive advisory lock on the
// sessions metadata file. All read-modify-write cycles must go through this so
// that concurrent devx processes (CLI, TUI, web daemon) cannot clobber each
// other's updates via a stale full-store overwrite.
//
// The lock is NOT reentrant: fn (and the callbacks passed to UpdateSession /
// Mutate, which run under this lock) must not call Save, UpdateSession, or
// Mutate, or they will deadlock trying to reacquire the lock.
func withSessionsLock(fn func() error) error {
	// Serialize in-process callers before taking the OS file lock. This avoids a
	// Windows LockFileEx self-deadlock when concurrent goroutines in one devx
	// process attempt to take conflicting byte-range locks on separate handles.
	// The file lock below still protects against other devx processes.
	sessionsProcessLock.Lock()
	defer sessionsProcessLock.Unlock()

	lockPath := getSessionsLockPath()
	if dir := filepath.Dir(lockPath); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory: %w", err)
		}
	}
	lock, err := acquireSessionsLock(lockPath)
	if err != nil {
		return err
	}
	defer lock.release()
	return fn()
}

// writeStoreAtomic marshals and writes the store to disk via a temp file and
// rename. On Unix (devx's supported runtime) the same-directory rename is
// atomic, so a concurrent lock-free reader never observes a partial file; the
// temp file is fsynced before rename so its contents are durable before it is
// published. Crash durability is best-effort: the parent directory entry is not
// fsynced, which is acceptable because sessions.json is reconstructible from
// running containers/worktrees, not a system of record. On Windows os.Rename is
// not guaranteed atomic (see lock_windows.go); that platform is best-effort.
// Callers must already hold the sessions lock.
func (s *SessionStore) writeStoreAtomic() error {
	sessionsPath := getSessionsPath()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal sessions: %w", err)
	}

	dir := filepath.Dir(sessionsPath)
	tmp, err := os.CreateTemp(dir, ".sessions-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp sessions file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write temp sessions file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to chmod temp sessions file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to sync temp sessions file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp sessions file: %w", err)
	}
	if err := os.Rename(tmpName, sessionsPath); err != nil {
		return fmt.Errorf("failed to write sessions file: %w", err)
	}
	return nil
}

// save overwrites the entire on-disk store with this in-memory snapshot under
// the sessions lock, writing atomically.
//
// It is deliberately UNEXPORTED: overwriting from an in-memory snapshot is the
// exact lost-update footgun this package was hardened against. A stale snapshot
// (loaded before another process wrote the file) silently clobbers the other
// writer's changes even though the write itself is atomic. Targeted mutations
// must go through the lock-guarded mutators (UpdateSession, RemoveSession,
// AssignSlot, Mutate), which re-read the latest store under the lock before
// mutating. The only safe callers of save are those that own a freshly
// constructed store with no concurrent writers to lose (see Overwrite).
func (s *SessionStore) save() error {
	return withSessionsLock(s.writeStoreAtomic)
}

// Overwrite replaces the entire on-disk store with this in-memory store under
// the sessions lock, writing atomically.
//
// Use this ONLY for a freshly constructed or fully-owned store (e.g. clearing
// the registry, or seeding a known state) where there are no concurrent writers
// whose updates could be lost. For targeted changes to an existing store, use
// UpdateSession / RemoveSession / AssignSlot / Mutate instead, which re-read
// under the lock and cannot drop concurrent updates.
func (s *SessionStore) Overwrite() error {
	return s.save()
}

// AddSession adds a new session to the store
func (s *SessionStore) AddSession(name, branch, path string, ports map[string]int) error {
	return s.Mutate(func(fresh *SessionStore) error {
		if _, exists := fresh.Sessions[name]; exists {
			return fmt.Errorf("session %s already exists", name)
		}

		now := time.Now()
		fresh.Sessions[name] = &Session{
			Name:      name,
			Branch:    branch,
			Path:      path,
			Ports:     ports,
			CreatedAt: now,
			UpdatedAt: now,
		}
		return nil
	})
}

// AddSessionWithProject adds a new session to the store with project information
func (s *SessionStore) AddSessionWithProject(name, branch, path string, ports map[string]int, projectAlias, projectPath string) error {
	return s.Mutate(func(fresh *SessionStore) error {
		if _, exists := fresh.Sessions[name]; exists {
			return fmt.Errorf("session %s already exists", name)
		}

		// Auto-assign color if not already set by caller
		color := AutoColor(name)

		now := time.Now()
		fresh.Sessions[name] = &Session{
			Name:         name,
			ProjectAlias: projectAlias,
			ProjectPath:  projectPath,
			Branch:       branch,
			Path:         path,
			Ports:        ports,
			Routes:       make(map[string]string),
			Color:        color,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		return nil
	})
}

// GetSession retrieves a session by name
func (s *SessionStore) GetSession(name string) (*Session, bool) {
	session, exists := s.Sessions[name]
	return session, exists
}

// UpdateSession updates an existing session.
//
// The mutation is applied under the sessions file lock against the LATEST
// on-disk store, not this (possibly stale) in-memory snapshot. This prevents
// lost updates when another devx process (TUI, web daemon, concurrent CLI) has
// written the file since this store was loaded. The in-memory receiver is
// refreshed to match what was persisted.
func (s *SessionStore) UpdateSession(name string, updateFn func(*Session)) error {
	return withSessionsLock(func() error {
		fresh, err := loadSessionsUnlocked()
		if err != nil {
			return err
		}
		session, exists := fresh.Sessions[name]
		if !exists {
			return fmt.Errorf("%w: %s", ErrSessionNotFound, name)
		}

		updateFn(session)
		session.UpdatedAt = time.Now()

		if err := fresh.writeStoreAtomic(); err != nil {
			return err
		}
		s.adoptFrom(fresh)
		return nil
	})
}

// MarkReviewed records an explicit stale-review/snooze marker without treating
// the review as real activity or changing UpdatedAt.
func MarkReviewed(name string, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	return withSessionsLock(func() error {
		fresh, err := loadSessionsUnlocked()
		if err != nil {
			return err
		}
		sess, exists := fresh.Sessions[name]
		if !exists {
			return fmt.Errorf("%w: %s", ErrSessionNotFound, name)
		}
		sess.LastReviewedAt = at
		return fresh.writeStoreAtomic()
	})
}

// Mutate applies fn to the LATEST on-disk store under the sessions file lock and
// persists the result atomically, then refreshes this in-memory store to match.
// Use this for create/remove/reconcile flows that change more than one session
// or the slot map, so concurrent writers are never clobbered.
func (s *SessionStore) Mutate(fn func(*SessionStore) error) error {
	return withSessionsLock(func() error {
		fresh, err := loadSessionsUnlocked()
		if err != nil {
			return err
		}
		if err := fn(fresh); err != nil {
			return err
		}
		if err := fresh.writeStoreAtomic(); err != nil {
			return err
		}
		s.adoptFrom(fresh)
		return nil
	})
}

// adoptFrom replaces this store's contents with another's, so callers holding a
// reference observe the freshly-persisted state.
func (s *SessionStore) adoptFrom(other *SessionStore) {
	s.Sessions = other.Sessions
	s.NumberedSlots = other.NumberedSlots
}

// RecordAttach updates the LastAttached timestamp for a session
func (s *SessionStore) RecordAttach(name string) error {
	return s.UpdateSession(name, func(sess *Session) {
		sess.LastAttached = time.Now()
	})
}

// RemoveSession removes a session from the store, re-reading the latest on-disk
// store under the lock so concurrent writers are not clobbered.
func (s *SessionStore) RemoveSession(name string) error {
	return s.Mutate(func(fresh *SessionStore) error {
		if _, exists := fresh.Sessions[name]; !exists {
			return fmt.Errorf("session %s not found", name)
		}
		delete(fresh.Sessions, name)
		return nil
	})
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
	// Freshly constructed empty store with no updates to lose -> Overwrite is safe.
	return store.Overwrite()
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
	// "=name" forces exact matching so "/" in session names is not treated as a
	// pane separator in tmux target syntax.
	cmd := exec.Command("tmux", "kill-session", "-t", "="+sessionName)
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

// SetAttentionFlag sets the attention flag for a session.
func SetAttentionFlag(sessionName, reason string) error {
	return SetAttentionFlagWithSource(sessionName, reason, "manual")
}

// SetAttentionFlagWithSource sets the attention flag with a structured source.
func SetAttentionFlagWithSource(sessionName, reason, source string) error {
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
		s.AttentionSource = source
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
		s.AttentionSource = ""
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
	var assigned int
	err := s.Mutate(func(fresh *SessionStore) error {
		if _, exists := fresh.Sessions[name]; !exists {
			return fmt.Errorf("session '%s' not found", name)
		}

		// Check if session already has a slot
		if slot := fresh.GetSlotForSession(name); slot != 0 {
			assigned = slot
			return nil
		}

		// Find lowest available slot (1-9)
		for i := 1; i <= 9; i++ {
			if _, taken := fresh.NumberedSlots[i]; !taken {
				fresh.NumberedSlots[i] = name
				assigned = i
				return nil
			}
		}

		// All slots full — evict the session with the oldest LastAttached.
		// Iterate slots in ascending order for deterministic tie-breaking.
		slots := make([]int, 0, len(fresh.NumberedSlots))
		for slot := range fresh.NumberedSlots {
			slots = append(slots, slot)
		}
		sort.Ints(slots)

		oldestSlot := 0
		var oldestTime time.Time
		for _, slot := range slots {
			sessName := fresh.NumberedSlots[slot]
			sess, exists := fresh.Sessions[sessName]
			if !exists {
				// Stale slot, use it immediately
				fresh.NumberedSlots[slot] = name
				assigned = slot
				return nil
			}
			if oldestSlot == 0 || sess.LastAttached.Before(oldestTime) {
				oldestSlot = slot
				oldestTime = sess.LastAttached
			}
		}

		fresh.NumberedSlots[oldestSlot] = name
		assigned = oldestSlot
		return nil
	})
	if err != nil {
		return 0, err
	}
	return assigned, nil
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
