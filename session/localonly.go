package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// LocalOnlyOwnerPiMCP is the only owner allowed to create local-only sessions.
const LocalOnlyOwnerPiMCP = "devx-pi-mcp"

// localOnlyTmuxEnv is set on the tmux session at creation (atomically, via
// new-session -e) so an existing tmux session can be proven to belong to the
// local-only session before it is adopted.
const localOnlyTmuxEnv = "DEVX_LOCAL_ONLY_AGENT"

// LocalOnlyWindow is the name of the inert first window of a local-only
// tmux session. It runs no shell and no project command.
const LocalOnlyWindow = "devx-local"

// LocalOnlyMeta marks a session that DevX created for a managed agent and
// must never expose. Local-only sessions have no allocated service ports,
// no routes, no generated .envrc/.tmuxp.yaml, and no project template
// windows; every route builder skips them even if ports or routes are
// later added to the record by hand.
type LocalOnlyMeta struct {
	Owner     string    `json:"owner"`
	AgentID   string    `json:"agent_id"`
	CreatedAt time.Time `json:"created_at"`
}

// IsLocalOnly reports whether the session carries a local-only marker. Any
// marker, even an incomplete one, counts: callers that route or expose
// sessions must fail closed.
func (s *Session) IsLocalOnly() bool { return s != nil && s.LocalOnly != nil }

// ErrLocalOnlyNotOwned is returned when a session or tmux session with the
// requested name exists but was not created for this agent.
var ErrLocalOnlyNotOwned = errors.New("not owned by this local-only agent")

// LocalOnlyRequest describes a local-only session to create (or resume).
type LocalOnlyRequest struct {
	Name         string
	ProjectAlias string
	ProjectPath  string
	AgentID      string
}

// CreateLocalOnlySession creates, or resumes an interrupted creation of, a
// local-only session: metadata first (with the marker, so the record is
// never routable at any instant), then a git worktree on a new branch named
// after the session. It never fetches, allocates ports, writes env files,
// copies bootstrap files, renders templates, syncs routes or starts tmux.
//
// Resuming is allowed only when the existing record carries a marker for the
// same agent. On worktree failure the owned record is removed again, so a
// failed first attempt leaves nothing behind.
func CreateLocalOnlySession(req LocalOnlyRequest) (*Session, error) {
	if !IsValidSessionName(req.Name) || strings.Contains(req.Name, "/") {
		return nil, fmt.Errorf("invalid local-only session name %q", req.Name)
	}
	if req.AgentID == "" || req.ProjectPath == "" || !filepath.IsAbs(req.ProjectPath) {
		return nil, fmt.Errorf("local-only session needs an agent id and an absolute project path")
	}
	worktree := filepath.Join(req.ProjectPath, ".worktrees", req.Name)
	store := &SessionStore{}
	created := false
	err := store.Mutate(func(fresh *SessionStore) error {
		if existing, ok := fresh.Sessions[req.Name]; ok {
			if !existing.IsLocalOnly() || existing.LocalOnly.Owner != LocalOnlyOwnerPiMCP || existing.LocalOnly.AgentID != req.AgentID {
				return fmt.Errorf("session %q: %w", req.Name, ErrLocalOnlyNotOwned)
			}
			if existing.Path != worktree || existing.ProjectPath != req.ProjectPath {
				return fmt.Errorf("session %q: recorded path does not match this start", req.Name)
			}
			return nil
		}
		if tmuxHasSession(req.Name) {
			return fmt.Errorf("tmux session %q already exists: %w", req.Name, ErrLocalOnlyNotOwned)
		}
		if _, err := os.Stat(worktree); err == nil {
			return fmt.Errorf("worktree path %s already exists: %w", worktree, ErrLocalOnlyNotOwned)
		}
		if exists, err := BranchExists(req.ProjectPath, req.Name); err != nil {
			return err
		} else if exists {
			return fmt.Errorf("branch %q already exists: %w", req.Name, ErrLocalOnlyNotOwned)
		}
		now := time.Now()
		fresh.Sessions[req.Name] = &Session{
			Name: req.Name, ProjectAlias: req.ProjectAlias, ProjectPath: req.ProjectPath,
			Branch: req.Name, Path: worktree, Ports: map[string]int{},
			CreatedAt: now, UpdatedAt: now, InstanceID: NewInstanceID(),
			LocalOnly: &LocalOnlyMeta{Owner: LocalOnlyOwnerPiMCP, AgentID: req.AgentID, CreatedAt: now},
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := ensureLocalOnlyWorktree(req.ProjectPath, worktree, req.Name); err != nil {
		if created {
			_ = removeOwnedLocalOnlyRecord(req.Name, req.AgentID)
		}
		return nil, err
	}
	sess, _ := store.GetSession(req.Name)
	if fresh, err := LoadSessions(); err == nil {
		if s, ok := fresh.GetSession(req.Name); ok {
			sess = s
		}
	}
	return sess, nil
}

// ensureLocalOnlyWorktree creates the worktree from the project's current
// HEAD, or accepts an existing registered worktree on the expected branch
// (a resumed creation). It never fetches or pulls.
func ensureLocalOnlyWorktree(repo, worktree, branch string) error {
	if _, err := os.Stat(worktree); err == nil {
		wts, err := ListWorktrees(repo)
		if err != nil {
			return err
		}
		for _, wt := range wts {
			if samePathLO(wt.Path, worktree) && wt.Branch == branch {
				return nil
			}
		}
		return fmt.Errorf("%s exists but is not the worktree for %q", worktree, branch)
	}
	exists, err := BranchExists(repo, branch)
	if err != nil {
		return err
	}
	args := []string{"worktree", "add", "-b", branch, worktree}
	if exists {
		// Only reachable on resume (the owned record exists): an earlier
		// attempt created the branch but not the checkout.
		args = []string{"worktree", "add", worktree, branch}
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeOwnedLocalOnlyRecord(name, agentID string) error {
	store := &SessionStore{}
	return store.Mutate(func(fresh *SessionStore) error {
		s, ok := fresh.Sessions[name]
		if ok && s.IsLocalOnly() && s.LocalOnly.AgentID == agentID {
			delete(fresh.Sessions, name)
			fresh.ReconcileSlots()
		}
		return nil
	})
}

// EnsureLocalOnlyTmuxSession makes sure the local-only session's tmux
// session exists. A new one gets a single inert window (no shell, no
// project command) and is tagged with the owning agent id at creation. An
// existing tmux session is adopted only if it carries that same tag.
func EnsureLocalOnlyTmuxSession(name string, sess *Session) error {
	if !sess.IsLocalOnly() || sess.LocalOnly.AgentID == "" || sess.LocalOnly.Owner != LocalOnlyOwnerPiMCP {
		return fmt.Errorf("session %q has no valid local-only marker", name)
	}
	if tmuxHasSession(name) {
		out, err := exec.Command("tmux", "show-environment", "-t", "="+name, localOnlyTmuxEnv).Output()
		if err != nil || strings.TrimSpace(string(out)) != localOnlyTmuxEnv+"="+sess.LocalOnly.AgentID {
			return fmt.Errorf("tmux session %q: %w", name, ErrLocalOnlyNotOwned)
		}
		return nil
	}
	if _, err := os.Stat(sess.Path); err != nil {
		return fmt.Errorf("worktree for %q is missing: %w", name, err)
	}
	out, err := exec.Command("tmux", "new-session", "-d", "-s", name, "-n", LocalOnlyWindow,
		"-c", sess.Path, "-e", localOnlyTmuxEnv+"="+sess.LocalOnly.AgentID,
		"tail", "-f", "/dev/null").CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func tmuxHasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// TmuxHasSession reports whether a tmux session with exactly this name exists.
func TmuxHasSession(name string) bool { return tmuxHasSession(name) }

func samePathLO(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 == nil && err2 == nil {
		return ra == rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
