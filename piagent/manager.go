package piagent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// PermissionError marks a request the caller is not allowed to make. MCP
// surfaces these distinctly from operational failures.
type PermissionError struct{ Msg string }

func (e *PermissionError) Error() string { return "permission denied: " + e.Msg }

func denied(format string, a ...any) error { return &PermissionError{Msg: fmt.Sprintf(format, a...)} }

// IsPermissionDenied reports whether err is a PermissionError.
func IsPermissionDenied(err error) bool {
	var p *PermissionError
	return errors.As(err, &p)
}

// CreatedSession is what a SessionCreator returns for a new DevX session.
type CreatedSession struct {
	Name     string
	Path     string
	Project  string
	TmuxName string
}

// SessionCreator creates and prepares new DevX sessions. The production
// implementation shells out to `devx session create`; tests use a temp dir.
type SessionCreator interface {
	// Create makes a new session. If the session already exists it may be
	// adopted only when it was created at or after notBefore (the time the
	// idempotency record was written), i.e. by an earlier interrupted attempt
	// of this same start. Anything older belongs to someone else: fail.
	Create(name, project string, notBefore time.Time) (CreatedSession, error)
	// EnsureTmux makes sure the session's tmux session exists.
	EnsureTmux(name string) error
	// Exists reports whether a DevX session with this name exists.
	Exists(name string) (bool, error)
}

// Config controls how agents are launched.
type Config struct {
	// PiCommand is the Pi executable. Defaults to "pi".
	PiCommand string
	// PiCommandArgs are placed right after PiCommand (e.g. when PiCommand is
	// a wrapper such as env). Human-owned configuration only.
	PiCommandArgs []string
	// PiArgs are extra arguments appended to the Pi command line. They come
	// from human-owned configuration, never from MCP callers.
	PiArgs []string
	// PassEnv names environment variables copied into the launch script.
	PassEnv []string
	// AllowedProjects is the allowlist for start. Empty denies all starts.
	AllowedProjects []string
	// DevxExecutable lets the bridge raise DevX attention flags. Optional.
	DevxExecutable string
	// WindowName is the tmux window name for agents.
	WindowName string
}

// Manager implements the managed-agent operations shared by MCP and CLI.
type Manager struct {
	Store   *Store
	Tmux    Tmux
	Creator SessionCreator
	Config  Config
}

func NewManager(store *Store, tmux Tmux, creator SessionCreator, cfg Config) *Manager {
	if cfg.PiCommand == "" {
		cfg.PiCommand = "pi"
	}
	if cfg.WindowName == "" {
		cfg.WindowName = "pi-agent"
	}
	return &Manager{Store: store, Tmux: tmux, Creator: creator, Config: cfg}
}

func hashKey(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:16])
}

func validatePrompt(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("prompt is required")
	}
	if len(p) > MaxPromptBytes {
		return fmt.Errorf("prompt is %d bytes; limit is %d", len(p), MaxPromptBytes)
	}
	if !utf8.ValidString(p) {
		return fmt.Errorf("prompt must be valid UTF-8")
	}
	return nil
}

func validateIdemKey(k string) error {
	if strings.TrimSpace(k) == "" {
		return fmt.Errorf("idempotency_key is required so retried dispatches cannot duplicate work")
	}
	if len(k) > 200 {
		return fmt.Errorf("idempotency_key is too long")
	}
	return nil
}

func (m *Manager) projectAllowed(p string) bool {
	for _, a := range m.Config.AllowedProjects {
		if a == p {
			return true
		}
	}
	return false
}

// StartRequest starts a new Pi agent in a NEW DevX session.
type StartRequest struct {
	Project        string
	SessionName    string
	Prompt         string
	IdempotencyKey string
	By             string
}

// StartResult identifies what was (or had already been) started.
type StartResult struct {
	AgentID     string `json:"agent_id"`
	TaskID      string `json:"task_id"`
	Session     string `json:"session"`
	PiSessionID string `json:"pi_session_id"`
	Replayed    bool   `json:"replayed"`
}

// Start is idempotent on IdempotencyKey: a retry after a dropped response or
// a crash mid-start returns (and finishes) the original start instead of
// creating a second session or task.
func (m *Manager) Start(req StartRequest) (*StartResult, error) {
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}
	if err := validatePrompt(req.Prompt); err != nil {
		return nil, err
	}
	if !m.projectAllowed(req.Project) {
		return nil, denied("project %q is not in the pi_mcp allowlist", req.Project)
	}
	keyHash := hashKey("start", req.IdempotencyKey)
	reqHash := hashKey(req.Project, req.SessionName, req.Prompt)
	var result *StartResult
	err := m.Store.withIdemLock(keyHash, func() error {
		rec, err := m.Store.loadIdem(keyHash)
		switch {
		case err == nil:
			if rec.RequestHash != reqHash {
				return fmt.Errorf("idempotency_key was already used for a different start request")
			}
			result = &StartResult{AgentID: rec.AgentID, TaskID: rec.TaskID, Session: rec.SessionName, PiSessionID: rec.PiSessionID, Replayed: true}
		case errors.Is(err, ErrNotFound):
			name := req.SessionName
			agentID := newAgentID()
			if name == "" {
				name = "pi-" + strings.TrimPrefix(agentID, "pa_")
			}
			exists, err := m.Creator.Exists(name)
			if err != nil {
				return err
			}
			if exists {
				return denied("session %q already exists; MCP may only start agents in new sessions", name)
			}
			rec = &idemRecord{Kind: "start", KeyHash: keyHash, RequestHash: reqHash, AgentID: agentID, TaskID: newTaskID(),
				PiSessionID: newPiSessionID(time.Now()), SessionName: name, CreatingSession: true, CreatedAt: m.Store.now()}
			// Persist the IDs before any side effect so a crash can resume
			// with the same identities.
			if err := m.Store.saveIdem(rec); err != nil {
				return err
			}
			result = &StartResult{AgentID: rec.AgentID, TaskID: rec.TaskID, Session: rec.SessionName, PiSessionID: rec.PiSessionID}
		default:
			return err
		}
		return m.completeStart(rec, req)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// completeStart performs (or resumes) each start step; every step is
// idempotent so a retry after a crash finishes the original start.
func (m *Manager) completeStart(rec *idemRecord, req StartRequest) error {
	agent, err := m.Store.LoadAgent(rec.AgentID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if agent == nil {
		created, err := m.Creator.Create(rec.SessionName, req.Project, rec.CreatedAt.Add(-2*time.Second))
		if err != nil {
			return fmt.Errorf("create session %q: %w", rec.SessionName, err)
		}
		now := m.Store.now()
		agent = &Agent{
			ID: rec.AgentID, DevxSession: created.Name, Project: created.Project, Worktree: created.Path,
			PiSessionID: rec.PiSessionID, Binding: Binding{TmuxSession: created.TmuxName},
			Lease:     Lease{Holder: LeaseManaged, Generation: 1, Since: now, Reason: "created"},
			CreatedAt: now, CreatedBy: req.By, StartTaskID: rec.TaskID, StartIdemHash: rec.KeyHash,
		}
		if err := m.Store.ensureAgentDirs(agent.ID); err != nil {
			return err
		}
		err = m.Store.WithAgentLock(agent.ID, func() error {
			if err := m.Store.SaveAgent(agent); err != nil {
				return err
			}
			_, err := m.Store.AppendEvent(agent.ID, Event{Type: "agent_created", Source: "devx", Data: map[string]any{"session": agent.DevxSession, "pi_session_id": agent.PiSessionID}})
			return err
		})
		if err != nil {
			return err
		}
	}
	if _, err := m.Store.LoadTask(agent.ID, rec.TaskID); errors.Is(err, ErrNotFound) {
		if err := m.Store.WithAgentLock(agent.ID, func() error {
			return m.enqueueLocked(agent.ID, rec.TaskID, req.Prompt, rec.KeyHash)
		}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if agent.Binding.PaneID == "" || !m.Tmux.Pane(agent.Binding.PaneID).Exists {
		return m.launch(agent.ID, false)
	}
	return nil
}

func (m *Manager) enqueueLocked(agentID, taskID, prompt, idemHash string) error {
	tasks, err := m.Store.ListTasks(agentID)
	if err != nil {
		return err
	}
	order := int64(1)
	if n := len(tasks); n > 0 {
		order = tasks[n-1].Order + 1
	}
	now := m.Store.now()
	t := &Task{ID: taskID, AgentID: agentID, Order: order, Prompt: prompt, IdempotencyHash: idemHash, State: TaskWaiting, CreatedAt: now}
	if err := m.Store.SaveTask(t); err != nil {
		return err
	}
	_, err = m.Store.AppendEvent(agentID, Event{Type: "task_queued", TaskID: taskID, Source: "devx", Data: map[string]any{"order": order, "prompt_bytes": len(prompt)}})
	return err
}

// launch starts Pi in a dedicated window (or respawns the bound pane) with a
// fresh launch nonce. Bridges started with an older nonce are fenced out.
func (m *Manager) launch(agentID string, relaunch bool) error {
	agent, err := m.Store.LoadAgent(agentID)
	if err != nil {
		return err
	}
	if err := m.Creator.EnsureTmux(agent.DevxSession); err != nil {
		return fmt.Errorf("ensure tmux session: %w", err)
	}
	nonce := randomHex(12)
	bridgePath, err := WriteBridge(m.Store.Root)
	if err != nil {
		return err
	}
	script := m.launchScript(agent, nonce, bridgePath)
	scriptPath := filepath.Join(m.Store.AgentDir(agent.ID), "launch.sh")
	if err := writeFileAtomic(scriptPath, []byte(script)); err != nil {
		return err
	}
	if err := os.Chmod(scriptPath, 0o700); err != nil {
		return err
	}
	// Record the nonce before the process starts so the bridge can verify it.
	err = m.Store.WithAgentLock(agent.ID, func() error {
		a, err := m.Store.LoadAgent(agent.ID)
		if err != nil {
			return err
		}
		a.LaunchNonce = nonce
		a.LaunchCount++
		if err := m.markOrphanedLocked(a, "Pi was relaunched while this task was running"); err != nil {
			return err
		}
		return m.Store.SaveAgent(a)
	})
	if err != nil {
		return err
	}
	pane := m.Tmux.Pane(agent.Binding.PaneID)
	tmuxName := agent.Binding.TmuxSession
	if tmuxName == "" {
		tmuxName = agent.DevxSession
	}
	var windowID, paneID string
	if relaunch && pane.Exists && pane.SessionName == tmuxName {
		if err := m.Tmux.RespawnPane(agent.Binding.PaneID, agent.Worktree, scriptPath); err != nil {
			return err
		}
		windowID, paneID = pane.WindowID, agent.Binding.PaneID
	} else {
		windowID, paneID, err = m.Tmux.NewWindow(tmuxName, m.Config.WindowName, agent.Worktree, scriptPath)
		if err != nil {
			return err
		}
	}
	return m.Store.WithAgentLock(agent.ID, func() error {
		a, err := m.Store.LoadAgent(agent.ID)
		if err != nil {
			return err
		}
		a.Binding = Binding{TmuxSession: tmuxName, WindowID: windowID, PaneID: paneID}
		if err := m.Store.SaveAgent(a); err != nil {
			return err
		}
		_, err = m.Store.AppendEvent(a.ID, Event{Type: "launched", Source: "devx", Data: map[string]any{"pane": paneID, "window": windowID, "launch": a.LaunchCount, "relaunch": relaunch}})
		return err
	})
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

func (m *Manager) launchScript(a *Agent, nonce, bridgePath string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# Generated by devx for managed Pi agent " + a.ID + ". Do not edit.\n")
	env := map[string]string{
		"DEVX_PI_AGENT_ROOT":   m.Store.Root,
		"DEVX_PI_AGENT_ID":     a.ID,
		"DEVX_PI_LAUNCH_NONCE": nonce,
		"DEVX_PI_DEVX_SESSION": a.DevxSession,
	}
	if m.Config.DevxExecutable != "" {
		env["DEVX_PI_DEVX_EXECUTABLE"] = m.Config.DevxExecutable
	}
	for _, k := range m.Config.PassEnv {
		if v, ok := os.LookupEnv(k); ok {
			env[k] = v
		}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(env[k]))
	}
	fmt.Fprintf(&b, "cd %s || exit 1\n", shellQuote(a.Worktree))
	args := append([]string{m.Config.PiCommand}, m.Config.PiCommandArgs...)
	args = append(args, "--session-id", a.PiSessionID, "--name", "devx "+a.DevxSession, "-e", bridgePath)
	args = append(args, m.Config.PiArgs...)
	quoted := make([]string, len(args))
	for i, s := range args {
		quoted[i] = shellQuote(s)
	}
	b.WriteString(strings.Join(quoted, " ") + "\n")
	b.WriteString("status=$?\n")
	fmt.Fprintf(&b, "echo\necho \"=== Pi exited (status $status). Managed agent %s is offline. ===\"\n", a.ID)
	fmt.Fprintf(&b, "echo \"Relaunch it with: devx agent relaunch %s\"\n", a.ID)
	// Hold the pane without a shell so nothing typed here can execute and
	// devx never has a shell it could mistake for Pi.
	b.WriteString("exec tail -f /dev/null\n")
	return b.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// SendRequest queues a follow-up prompt for an existing managed agent.
type SendRequest struct {
	AgentID        string
	Prompt         string
	IdempotencyKey string
}

// SendResult describes the queued (or previously queued) task.
type SendResult struct {
	TaskID   string `json:"task_id"`
	Replayed bool   `json:"replayed"`
}

func (m *Manager) Send(req SendRequest) (*SendResult, error) {
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}
	if err := validatePrompt(req.Prompt); err != nil {
		return nil, err
	}
	if _, err := m.Store.LoadAgent(req.AgentID); err != nil {
		return nil, err
	}
	keyHash := hashKey("send", req.AgentID, req.IdempotencyKey)
	reqHash := hashKey(req.Prompt)
	var out *SendResult
	err := m.Store.withIdemLock(keyHash, func() error {
		rec, err := m.Store.loadIdem(keyHash)
		if err == nil {
			if rec.RequestHash != reqHash {
				return fmt.Errorf("idempotency_key was already used for a different prompt")
			}
			out = &SendResult{TaskID: rec.TaskID, Replayed: true}
			// Finish an enqueue interrupted after the record was written.
			if _, err := m.Store.LoadTask(req.AgentID, rec.TaskID); errors.Is(err, ErrNotFound) {
				return m.Store.WithAgentLock(req.AgentID, func() error {
					return m.enqueueLocked(req.AgentID, rec.TaskID, req.Prompt, keyHash)
				})
			}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		rec = &idemRecord{Kind: "send", KeyHash: keyHash, RequestHash: reqHash, AgentID: req.AgentID, TaskID: newTaskID(), CreatedAt: m.Store.now()}
		if err := m.Store.saveIdem(rec); err != nil {
			return err
		}
		out = &SendResult{TaskID: rec.TaskID}
		return m.Store.WithAgentLock(req.AgentID, func() error {
			return m.enqueueLocked(req.AgentID, rec.TaskID, req.Prompt, keyHash)
		})
	})
	return out, err
}

// Cancel cancels a waiting task immediately, or asks the bridge to abort a
// running one. While a human holds control, running work belongs to the
// human and remote cancellation is denied.
func (m *Manager) Cancel(taskID string) (*Task, error) {
	t, err := m.Store.FindTask(taskID)
	if err != nil {
		return nil, err
	}
	var out *Task
	err = m.Store.WithAgentLock(t.AgentID, func() error {
		a, err := m.Store.LoadAgent(t.AgentID)
		if err != nil {
			return err
		}
		t, err = m.Store.LoadTask(t.AgentID, taskID)
		if err != nil {
			return err
		}
		out = t
		switch t.State {
		case TaskWaiting:
			now := m.Store.now()
			t.State, t.FinishedAt, t.Note = TaskCancelled, &now, "cancelled before delivery"
			if err := m.Store.SaveTask(t); err != nil {
				return err
			}
			_, err = m.Store.AppendEvent(t.AgentID, Event{Type: "task_cancelled", TaskID: t.ID, Source: "devx", Data: map[string]any{"delivered": false}})
			return err
		case TaskRunning:
			if a.Lease.Holder == LeaseHuman {
				return denied("a human has control of agent %s; running work can only be stopped from the Pi session", a.ID)
			}
			if t.CancelRequested {
				return nil
			}
			t.CancelRequested = true
			if err := m.Store.SaveTask(t); err != nil {
				return err
			}
			_, err = m.Store.AppendEvent(t.AgentID, Event{Type: "cancel_requested", TaskID: t.ID, Source: "devx"})
			return err
		}
		return nil // already terminal
	})
	return out, err
}

// Takeover gives a human control. Queued remote work stops being delivered
// until Release. A task already delivered keeps running in Pi, where the
// human can see and interrupt it.
func (m *Manager) Takeover(agentID, by string) (*Agent, error) {
	return m.setLease(agentID, LeaseHuman, "explicit_takeover", by, false)
}

// Release returns control to managed dispatch. Only humans (CLI or the Pi
// TUI command) can release; it is deliberately not exposed over MCP.
func (m *Manager) Release(agentID, by string, dropQueued bool) (*Agent, error) {
	return m.setLease(agentID, LeaseManaged, "explicit_release", by, dropQueued)
}

func (m *Manager) setLease(agentID, holder, reason, by string, dropQueued bool) (*Agent, error) {
	var out *Agent
	err := m.Store.WithAgentLock(agentID, func() error {
		a, err := m.Store.LoadAgent(agentID)
		if err != nil {
			return err
		}
		out = a
		if dropQueued {
			tasks, err := m.Store.ListTasks(agentID)
			if err != nil {
				return err
			}
			for _, t := range tasks {
				if t.State != TaskWaiting {
					continue
				}
				now := m.Store.now()
				t.State, t.FinishedAt, t.Note = TaskCancelled, &now, "dropped by human on release"
				if err := m.Store.SaveTask(t); err != nil {
					return err
				}
				if _, err := m.Store.AppendEvent(agentID, Event{Type: "task_cancelled", TaskID: t.ID, Source: "human", Data: map[string]any{"delivered": false, "reason": "dropped_on_release"}}); err != nil {
					return err
				}
			}
		}
		if a.Lease.Holder == holder {
			return nil
		}
		prev := a.Lease.Holder
		a.Lease = Lease{Holder: holder, Generation: a.Lease.Generation + 1, Since: m.Store.now(), Reason: reason, By: by}
		if err := m.Store.SaveAgent(a); err != nil {
			return err
		}
		_, err = m.Store.AppendEvent(agentID, Event{Type: "lease_changed", Source: "human", Data: map[string]any{"from": prev, "to": holder, "generation": a.Lease.Generation, "reason": reason}})
		return err
	})
	return out, err
}

// Relaunch restarts Pi for an agent whose process exited, resuming the same
// Pi session ID so conversation history and identity are preserved.
func (m *Manager) Relaunch(agentID string, force bool) error {
	view, err := m.AgentStatus(agentID)
	if err != nil {
		return err
	}
	if view.BridgeOnline && !force {
		return fmt.Errorf("agent %s is running in pane %s; refusing to relaunch a live Pi (use --force to replace it)", agentID, view.Agent.Binding.PaneID)
	}
	return m.launch(agentID, true)
}

// markOrphanedLocked moves running tasks whose bridge instance is gone to
// unknown: they were delivered, so they may have done work, but no live
// process can report their outcome.
func (m *Manager) markOrphanedLocked(a *Agent, note string) error {
	tasks, err := m.Store.ListTasks(a.ID)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if t.State != TaskRunning {
			continue
		}
		now := m.Store.now()
		t.State, t.FinishedAt, t.Note = TaskUnknown, &now, note
		if err := m.Store.SaveTask(t); err != nil {
			return err
		}
		if _, err := m.Store.AppendEvent(a.ID, Event{Type: "task_unknown", TaskID: t.ID, Source: "devx", Data: map[string]any{"reason": note}}); err != nil {
			return err
		}
	}
	return nil
}

// AgentView is the reconciled status of an agent.
type AgentView struct {
	Agent        *Agent  `json:"agent"`
	State        string  `json:"state"`
	BridgeOnline bool    `json:"bridge_online"`
	Bridge       *Bridge `json:"bridge,omitempty"`
	PaneAlive    bool    `json:"pane_alive"`
	BindingOK    bool    `json:"binding_ok"`
	Detail       string  `json:"detail,omitempty"`
	CurrentTask  string  `json:"current_task,omitempty"`
	Waiting      int     `json:"waiting_tasks"`
	LastSeq      int64   `json:"last_event_seq"`
	AttachHint   string  `json:"attach_hint"`
}

// AgentStatus reconciles durable state with tmux and the bridge heartbeat.
// It is safe to call after an MCP server restart: nothing lives in memory.
func (m *Manager) AgentStatus(agentID string) (*AgentView, error) {
	var view *AgentView
	err := m.Store.WithAgentLock(agentID, func() error {
		a, err := m.Store.LoadAgent(agentID)
		if err != nil {
			return err
		}
		v := &AgentView{Agent: a, AttachHint: fmt.Sprintf("devx session attach %s  (window %q); take control with: devx agent takeover %s", a.DevxSession, m.Config.WindowName, a.ID)}
		pane := m.Tmux.Pane(a.Binding.PaneID)
		v.PaneAlive = pane.Exists && !pane.Dead
		v.BindingOK = pane.Exists && pane.SessionName == a.Binding.TmuxSession && pane.WindowID == a.Binding.WindowID
		b, berr := m.Store.LoadBridge(a.ID)
		bridgeDead := false
		if berr == nil {
			v.Bridge = b
			fresh := time.Since(b.Heartbeat) < BridgeStaleAfter
			v.BridgeOnline = fresh && b.Pane == a.Binding.PaneID && b.Nonce == a.LaunchNonce && b.Mode != "fenced" && b.Mode != "stopped"
			// The process that wrote the heartbeat is gone (crash or kill -9:
			// no shutdown hook ran). The pane may still exist because the
			// launch script holds it open for inspection.
			bridgeDead = b.Mode == "stopped" || (!fresh && !processAlive(b.PID))
		}
		tasks, err := m.Store.ListTasks(a.ID)
		if err != nil {
			return err
		}
		running := ""
		for _, t := range tasks {
			if t.State == TaskWaiting {
				v.Waiting++
			}
			if t.State == TaskRunning {
				running = t.ID
				// A different live bridge instance cannot be holding this
				// turn, and a missing pane cannot finish it.
				sameInstanceDead := bridgeDead && v.Bridge.Instance == t.BridgeInstance
				if (v.BridgeOnline && v.Bridge.Instance != t.BridgeInstance) || sameInstanceDead || (!pane.Exists || pane.Dead) && a.Binding.PaneID != "" {
					if err := m.markOrphanedLocked(a, "the Pi process that received this task is gone; outcome unknown — inspect the session"); err != nil {
						return err
					}
					running = ""
				}
			}
		}
		v.CurrentTask = running
		switch {
		case a.Binding.PaneID == "":
			v.State, v.Detail = AgentNotLaunched, "start did not finish launching Pi; retry the start with the same idempotency_key"
		case !pane.Exists:
			v.State, v.Detail = AgentPaneExited, "the bound tmux pane no longer exists"
		case !v.BindingOK:
			v.State, v.Detail = AgentStaleBind, fmt.Sprintf("pane %s now belongs to %s/%s, not the bound window", a.Binding.PaneID, pane.SessionName, pane.WindowID)
		case !v.BridgeOnline && bridgeDead:
			v.State, v.Detail = AgentPaneExited, "Pi exited; the pane is holding for inspection (devx agent relaunch resumes the same Pi session)"
		case !v.BridgeOnline:
			v.State, v.Detail = AgentUnknown, "the Pi bridge has not reported recently (starting, blocked, or exited)"
		case a.Lease.Holder == LeaseHuman:
			v.State = AgentHuman
		case running != "" || !v.Bridge.Idle:
			v.State = AgentRunning
		default:
			v.State = AgentIdle
		}
		_, v.LastSeq, _ = m.Store.ReadEvents(a.ID, 1<<62, 1)
		view = v
		return nil
	})
	return view, err
}

// TaskView is the client-facing status of one task.
type TaskView struct {
	TaskID          string     `json:"task_id"`
	AgentID         string     `json:"agent_id"`
	State           string     `json:"state"`
	WaitingReason   string     `json:"waiting_reason,omitempty"`
	CancelRequested bool       `json:"cancel_requested,omitempty"`
	HumanIntervened bool       `json:"human_intervened,omitempty"`
	StopReason      string     `json:"stop_reason,omitempty"`
	Error           string     `json:"error,omitempty"`
	Note            string     `json:"note,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	DeliveredAt     *time.Time `json:"delivered_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	ResultBytes     int        `json:"result_bytes,omitempty"`
	Excerpt         string     `json:"result_excerpt,omitempty"`
	ExcerptTrunc    bool       `json:"result_excerpt_truncated,omitempty"`
}

func (m *Manager) TaskStatus(taskID string) (*TaskView, *AgentView, error) {
	t, err := m.Store.FindTask(taskID)
	if err != nil {
		return nil, nil, err
	}
	av, err := m.AgentStatus(t.AgentID)
	if err != nil {
		return nil, nil, err
	}
	t, err = m.Store.LoadTask(t.AgentID, taskID) // re-read after reconciliation
	if err != nil {
		return nil, nil, err
	}
	tv := &TaskView{TaskID: t.ID, AgentID: t.AgentID, State: t.State, CancelRequested: t.CancelRequested, HumanIntervened: t.HumanIntervened,
		StopReason: t.StopReason, Error: Redact(t.Error), Note: t.Note, CreatedAt: t.CreatedAt, DeliveredAt: t.DeliveredAt, FinishedAt: t.FinishedAt}
	if t.State == TaskWaiting {
		tv.WaitingReason = m.waitingReason(av, t)
	}
	if text, ok, err := m.Store.ReadResult(t.AgentID, t.ID); err != nil {
		return nil, nil, err
	} else if ok {
		tv.ResultBytes = len(text)
		tv.Excerpt, tv.ExcerptTrunc = tailExcerpt(text, MaxExcerptBytes)
	}
	return tv, av, nil
}

func (m *Manager) waitingReason(av *AgentView, t *Task) string {
	switch {
	case av.Agent.Lease.Holder == LeaseHuman:
		return WaitHumanControl
	case av.State == AgentStaleBind || av.State == AgentPaneExited:
		return WaitBindingStale
	case !av.BridgeOnline:
		return WaitBridgeOnline
	case av.CurrentTask != "":
		return WaitQueued
	case av.Bridge != nil && !av.Bridge.Idle:
		return WaitBusy
	}
	tasks, _ := m.Store.ListTasks(t.AgentID)
	for _, o := range tasks {
		if o.State == TaskWaiting && o.Order < t.Order {
			return WaitQueued
		}
	}
	if av.Bridge != nil && av.Bridge.HumanTyping {
		return WaitBusy
	}
	return WaitDelivering
}

// ResultChunk is a cursor-addressed slice of a task's final text.
type ResultChunk struct {
	TaskID     string `json:"task_id"`
	State      string `json:"state"`
	Available  bool   `json:"available"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
	TotalBytes int    `json:"total_bytes"`
	EOF        bool   `json:"eof"`
	Text       string `json:"text"`
}

// Result returns up to maxBytes of the (redacted) final text from offset.
func (m *Manager) Result(taskID string, offset, maxBytes int) (*ResultChunk, error) {
	tv, _, err := m.TaskStatus(taskID)
	if err != nil {
		return nil, err
	}
	if maxBytes <= 0 || maxBytes > MaxResultChunk {
		maxBytes = MaxResultChunk
	}
	text, ok, err := m.Store.ReadResult(tv.AgentID, taskID)
	if err != nil {
		return nil, err
	}
	c := &ResultChunk{TaskID: taskID, State: tv.State, Available: ok, Offset: offset, TotalBytes: len(text)}
	if offset < 0 || offset > len(text) {
		return nil, fmt.Errorf("offset %d is outside the result (0..%d)", offset, len(text))
	}
	for offset > 0 && offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset--
	}
	c.Offset = offset
	chunk, _ := headExcerpt(text[offset:], maxBytes)
	c.Text = chunk
	c.NextOffset = offset + len(chunk)
	c.EOF = c.NextOffset >= len(text)
	return c, nil
}

// Events returns events after the cursor. With wait > 0 it blocks (by
// watching the local log, not by polling any model) until a new event is
// written or wait elapses.
func (m *Manager) Events(agentID string, after int64, limit int, wait time.Duration) ([]Event, int64, error) {
	if _, err := m.Store.LoadAgent(agentID); err != nil {
		return nil, 0, err
	}
	if wait > 30*time.Second {
		wait = 30 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		evs, last, err := m.Store.ReadEvents(agentID, after, limit)
		if err != nil || len(evs) > 0 || time.Now().After(deadline) {
			for i := range evs {
				evs[i].Data = redactData(evs[i].Data)
			}
			return evs, last, err
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func redactData(d map[string]any) map[string]any {
	for k, v := range d {
		if s, ok := v.(string); ok {
			d[k] = Redact(s)
		}
	}
	return d
}

// List returns reconciled views for all managed agents.
func (m *Manager) List() ([]*AgentView, error) {
	agents, err := m.Store.ListAgents()
	if err != nil {
		return nil, err
	}
	out := make([]*AgentView, 0, len(agents))
	for _, a := range agents {
		v, err := m.AgentStatus(a.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Inspect returns a read-only snapshot of the agent's pane.
func (m *Manager) Inspect(agentID string, lines int) (string, error) {
	a, err := m.Store.LoadAgent(agentID)
	if err != nil {
		return "", err
	}
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	return m.Tmux.Capture(a.Binding.PaneID, lines)
}

// itoa is used by tests and CLI helpers.
func itoa(i int64) string { return strconv.FormatInt(i, 10) }
