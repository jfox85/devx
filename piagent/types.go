// Package piagent manages Pi coding-agent sessions that DevX launches on
// behalf of a local MCP client while keeping them visible to, and
// controllable by, the human.
//
// Design (see docs/pi-mcp.md for the full write-up):
//
//   - Each managed agent is one interactive Pi TUI running in a dedicated tmux
//     window of a normal DevX session. There is no hidden headless process: the
//     pane a human attaches to is the process that receives remote prompts.
//   - A bridge extension (bridge/devx-bridge.ts) loaded into that Pi process
//     delivers queued dispatches with pi.sendUserMessage and reports results. It
//     never goes through tmux keystrokes, so prompts are never typed into a shell
//     or a human's half-written input.
//   - A lease in agent.json decides who drives the agent. Human input in the TUI,
//     or an explicit takeover, moves the lease to the human; queued remote
//     dispatches then wait until a human releases control.
//   - All shared state changes happen under one per-agent lock that the Go side
//     and the Node bridge both honor.
package piagent

import "time"

// Lease holders.
const (
	LeaseManaged = "managed"
	LeaseHuman   = "human"
)

// Task states exposed to MCP clients.
const (
	TaskWaiting   = "waiting"   // accepted, not yet delivered to Pi
	TaskRunning   = "running"   // delivered to Pi; turn in progress
	TaskCompleted = "completed" // Pi settled with a normal stop
	TaskFailed    = "failed"    // Pi settled with an error
	TaskCancelled = "cancelled" // cancelled before delivery, or aborted
	TaskUnknown   = "unknown"   // delivered, but the Pi process that held it is gone
)

// Waiting reasons explain why a waiting task has not been delivered.
const (
	WaitHumanControl = "human_control"
	WaitBusy         = "busy"
	WaitQueued       = "queued_behind_other_task"
	WaitBridgeOnline = "bridge_starting_or_offline"
	WaitBindingStale = "pane_binding_stale"
	WaitDelivering   = "pending_delivery" // eligible; the bridge picks it up on its next tick
)

// Agent states.
const (
	AgentIdle        = "idle"
	AgentRunning     = "running"
	AgentHuman       = "human_control"
	AgentUnknown     = "unknown"
	AgentStaleBind   = "binding_stale"
	AgentPaneExited  = "pane_exited"
	AgentNotLaunched = "not_launched"
	AgentRetired     = "retired"
	// AgentAdoptedPending: an existing human Pi was adopted but is still the
	// original process without the DevX bridge; a relaunch (same Pi session)
	// is needed before tasks can be delivered.
	AgentAdoptedPending = "adopted_pending_relaunch"
)

const (
	// MaxPromptBytes bounds a dispatched prompt.
	MaxPromptBytes = 64 << 10
	// MaxExcerptBytes bounds result excerpts in status/send responses.
	MaxExcerptBytes = 4 << 10
	// MaxResultChunk bounds one cursor read of a full result.
	MaxResultChunk = 16 << 10
	// MaxEventsPage bounds one page of events.
	MaxEventsPage = 200
	// BridgeStaleAfter is how old a bridge heartbeat may be before the
	// bridge is considered offline.
	BridgeStaleAfter = 5 * time.Second
)

// Lease records who currently drives an agent. Generation increments on every
// change so clients can detect that control moved underneath them.
type Lease struct {
	Holder     string    `json:"holder"`
	Generation int64     `json:"generation"`
	Since      time.Time `json:"since"`
	Reason     string    `json:"reason,omitempty"`
	By         string    `json:"by,omitempty"`
}

// Binding is the exact tmux location of the agent's Pi process.
type Binding struct {
	TmuxSession string `json:"tmux_session"`
	WindowID    string `json:"window_id"`
	PaneID      string `json:"pane_id"`
}

// Agent is the durable record for one managed Pi session.
type Agent struct {
	ID            string    `json:"id"`
	DevxSession   string    `json:"devx_session"`
	Project       string    `json:"project,omitempty"`
	Worktree      string    `json:"worktree"`
	PiSessionID   string    `json:"pi_session_id"`
	Binding       Binding   `json:"binding"`
	Lease         Lease     `json:"lease"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LaunchCount   int       `json:"launch_count"`
	LaunchNonce   string    `json:"launch_nonce,omitempty"`
	CreatedBy     string    `json:"created_by,omitempty"`
	StartTaskID   string    `json:"start_task_id,omitempty"`
	StartIdemHash string    `json:"start_idempotency_hash,omitempty"`
	// RetiredAt is set when the agent's DevX session was removed. A retired
	// agent keeps its records for inspection but accepts no new work.
	RetiredAt *time.Time `json:"retired_at,omitempty"`
	// Adopted marks an agent registered for an existing human-created DevX
	// session and Pi (devx agent adopt) rather than created by Start. Its
	// session is the owner's normal session: DevX never creates windows in
	// it and only ever respawns the exact adopted pane.
	Adopted bool `json:"adopted,omitempty"`
	// SessionInstanceID is the instance id of the exact DevX session record
	// this agent was bound to (at start or adoption, or by the reviewed
	// `devx session instances` migration). A session recreated under the same
	// name has a different id, so the agent is never bound to it implicitly.
	// Empty for agents from before instance ids that were not migrated.
	SessionInstanceID string `json:"session_instance_id,omitempty"`
	// SessionCreatedAt is the bound session record's created_at. It is the
	// identity witness when an older DevX writer dropped the record's
	// instance_id (see session.MatchesBoundInstance): authorization-relevant.
	SessionCreatedAt time.Time `json:"session_created_at,omitempty"`
}

// Task is one dispatched prompt.
type Task struct {
	ID              string     `json:"id"`
	AgentID         string     `json:"agent_id"`
	Order           int64      `json:"order"`
	Prompt          string     `json:"prompt"`
	IdempotencyHash string     `json:"idempotency_hash"`
	State           string     `json:"state"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeliveredAt     *time.Time `json:"delivered_at,omitempty"`
	ConfirmedAt     *time.Time `json:"confirmed_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	BridgeInstance  string     `json:"bridge_instance,omitempty"`
	LeaseGeneration int64      `json:"lease_generation_at_delivery,omitempty"`
	CancelRequested bool       `json:"cancel_requested,omitempty"`
	HumanIntervened bool       `json:"human_intervened,omitempty"`
	StopReason      string     `json:"stop_reason,omitempty"`
	Error           string     `json:"error,omitempty"`
	ResultBytes     int64      `json:"result_bytes,omitempty"`
	Note            string     `json:"note,omitempty"`
}

// Terminal reports whether the task will not change state again.
func (t *Task) Terminal() bool {
	switch t.State {
	case TaskCompleted, TaskFailed, TaskCancelled, TaskUnknown:
		return true
	}
	return false
}

// Bridge is the heartbeat the extension writes from inside Pi.
type Bridge struct {
	Instance      string    `json:"instance"`
	Nonce         string    `json:"nonce"`
	HumanTyping   bool      `json:"human_typing,omitempty"`
	PID           int       `json:"pid"`
	Pane          string    `json:"pane"`
	PiSessionID   string    `json:"pi_session_id"`
	PiSessionFile string    `json:"pi_session_file,omitempty"`
	Idle          bool      `json:"idle"`
	CurrentTask   string    `json:"current_task,omitempty"`
	Heartbeat     time.Time `json:"heartbeat"`
	Mode          string    `json:"mode,omitempty"`
}

// Event is one entry in an agent's durable, cursor-addressable event log.
type Event struct {
	Seq    int64          `json:"seq"`
	Time   time.Time      `json:"time"`
	Type   string         `json:"type"`
	TaskID string         `json:"task_id,omitempty"`
	Source string         `json:"source"`
	Data   map[string]any `json:"data,omitempty"`
}
