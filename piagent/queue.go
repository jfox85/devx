package piagent

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Pending-work controls: inspect the queue, edit or replace a prompt that Pi
// has not received, and cancel with an optional revision check.
//
// Invariants (see docs/pi-mcp.md "Queue controls"):
//   - Only tasks still in the DevX queue (state waiting) can be edited or
//     superseded. The bridge claims a task under the same agent lock and
//     that claim bumps the revision, so an edit can never change a prompt
//     Pi already has.
//   - Edits are compare-and-swap on the task revision. A mismatch is a
//     ConflictError carrying the current revision and state; nothing is
//     overwritten.
//   - Edits are idempotent on idempotency_key: a retry with the same key
//     and request returns the original result; the same key with a
//     different request is an error.
//   - Nothing here reads or writes the Pi editor or sends keystrokes.

// ErrConflict reports a failed revision check.
var ErrConflict = errors.New("conflict")

// ConflictError explains a failed compare-and-swap.
type ConflictError struct {
	TaskID   string
	Expected int64
	Current  int64
	State    string
	Phase    string
	Reason   string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict on task %s: %s (expected revision %d, current revision %d, state %s, phase %s)",
		e.TaskID, e.Reason, e.Expected, e.Current, e.State, e.Phase)
}

func (e *ConflictError) Unwrap() error { return ErrConflict }

// MaxEditsPerTask bounds in-place edits of one task. Every applied edit's
// idempotency mark is kept for the task's whole life (never evicted), in
// the same atomic write as the edit, so a retry with an old key is always
// recognised. Instead of evicting marks, a further edit is refused:
// supersede the task (a new task with its own record) to keep changing it.
const MaxEditsPerTask = 32

// ErrEditLimit reports that a task reached MaxEditsPerTask.
var ErrEditLimit = errors.New("edit limit reached")

// loadAgentTaskLocked loads a task and checks it belongs to agentID, so a
// caller holding a stale or wrong agent id cannot touch another agent's
// work. Callers hold the agent lock.
func (m *Manager) loadAgentTaskLocked(agentID, taskID string) (*Agent, *Task, error) {
	if !ValidAgentID(agentID) {
		return nil, nil, fmt.Errorf("invalid agent id %q", agentID)
	}
	a, err := m.Store.LoadAgent(agentID)
	if err != nil {
		return nil, nil, err
	}
	t, err := m.Store.LoadTask(agentID, taskID)
	if err != nil {
		return nil, nil, err
	}
	if t.AgentID != agentID {
		return nil, nil, fmt.Errorf("task %s: %w", taskID, ErrNotFound)
	}
	return a, t, nil
}

func checkPending(t *Task, expected int64, what string) error {
	if t.State != TaskWaiting {
		reason := what + " is only possible before delivery; the task was already handed to Pi"
		if t.Terminal() {
			reason = what + " is not possible: the task is " + t.State
		}
		return &ConflictError{TaskID: t.ID, Expected: expected, Current: t.Revision, State: t.State, Phase: t.Phase(), Reason: reason}
	}
	if t.Revision != expected {
		return &ConflictError{TaskID: t.ID, Expected: expected, Current: t.Revision, State: t.State, Phase: t.Phase(),
			Reason: "the task changed since you read it; re-read it with pi_queue and retry with the current revision"}
	}
	return nil
}

// editMark is recorded on a task so a retried edit is recognised. It binds
// the key to the request, so reusing a key for a different edit is caught.
func editMark(keyHash, reqHash string) string { return keyHash + ":" + reqHash }

// findEditMark reports whether keyHash was applied to t, and whether with
// the same request.
func findEditMark(t *Task, keyHash, reqHash string) (found, same bool) {
	for _, k := range t.EditKeys {
		if strings.HasPrefix(k, keyHash+":") {
			return true, k == editMark(keyHash, reqHash)
		}
	}
	return false, false
}

func addEditMark(t *Task, keyHash, reqHash string) {
	t.EditKeys = append(t.EditKeys, editMark(keyHash, reqHash))
}

// UpdatePendingRequest replaces the prompt of a task Pi has not received.
type UpdatePendingRequest struct {
	AgentID          string
	TaskID           string
	Prompt           string
	ExpectedRevision int64
	IdempotencyKey   string
}

// EditResult is the outcome of an edit.
type EditResult struct {
	TaskID   string `json:"task_id"`
	Revision int64  `json:"revision"`
	Replayed bool   `json:"replayed"`
	// ReplacementTaskID is set by supersede.
	ReplacementTaskID string `json:"replacement_task_id,omitempty"`
}

// UpdatePending edits a pending task's prompt in place (same task id and
// queue position). The applied key is stored in the same atomic write as
// the edit, so a crash cannot apply an edit without recording it.
func (m *Manager) UpdatePending(req UpdatePendingRequest) (*EditResult, error) {
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}
	if err := validatePrompt(req.Prompt); err != nil {
		return nil, err
	}
	keyHash := hashKey("update", req.AgentID, req.TaskID, req.IdempotencyKey)
	reqHash := hashKey(req.Prompt, fmt.Sprint(req.ExpectedRevision))
	var out *EditResult
	err := m.Store.WithAgentLock(req.AgentID, func() error {
		a, t, err := m.loadAgentTaskLocked(req.AgentID, req.TaskID)
		if err != nil {
			return err
		}
		if found, same := findEditMark(t, keyHash, reqHash); found {
			if !same {
				return fmt.Errorf("idempotency_key was already used for a different update of task %s", t.ID)
			}
			out = &EditResult{TaskID: t.ID, Revision: t.Revision, Replayed: true}
			return nil
		}
		if a.RetiredAt != nil {
			return denied("agent %s is retired: its session %q was removed", a.ID, a.DevxSession)
		}
		if err := checkPending(t, req.ExpectedRevision, "updating a prompt"); err != nil {
			return err
		}
		if len(t.EditKeys) >= MaxEditsPerTask {
			return fmt.Errorf("%w: task %s was already edited %d times; use pi_supersede_pending to replace it", ErrEditLimit, t.ID, MaxEditsPerTask)
		}
		prevBytes := len(t.Prompt)
		t.Prompt = req.Prompt
		addEditMark(t, keyHash, reqHash)
		if err := m.Store.SaveTask(t); err != nil {
			return err
		}
		out = &EditResult{TaskID: t.ID, Revision: t.Revision}
		_, err = m.Store.AppendEvent(a.ID, Event{Type: "task_updated", TaskID: t.ID, Source: "devx",
			Data: map[string]any{"revision": t.Revision, "prompt_bytes": len(req.Prompt), "previous_prompt_bytes": prevBytes}})
		return err
	})
	return out, err
}

// SupersedePendingRequest cancels a pending task and queues a replacement
// in its queue position.
type SupersedePendingRequest struct {
	AgentID          string
	TaskID           string
	Prompt           string
	ExpectedRevision int64
	IdempotencyKey   string
}

// SupersedePending atomically (under the agent lock) cancels a pending task
// and queues a replacement with the same queue order. The replacement id
// is fixed in an idempotency record written under the same lock before any
// task changes, and the old task is cancelled before the replacement is
// created, so a crash leaves at most one of them waiting and a retry
// completes the same replacement.
func (m *Manager) SupersedePending(req SupersedePendingRequest) (*EditResult, error) {
	if err := validateIdemKey(req.IdempotencyKey); err != nil {
		return nil, err
	}
	if err := validatePrompt(req.Prompt); err != nil {
		return nil, err
	}
	keyHash := hashKey("supersede", req.AgentID, req.TaskID, req.IdempotencyKey)
	reqHash := hashKey(req.Prompt, fmt.Sprint(req.ExpectedRevision))
	var out *EditResult
	err := m.Store.withIdemLock(keyHash, func() error {
		return m.Store.WithAgentLock(req.AgentID, func() error {
			a, old, err := m.loadAgentTaskLocked(req.AgentID, req.TaskID)
			if err != nil {
				return err
			}
			rec, err := m.Store.loadIdem(keyHash)
			switch {
			case err == nil:
				if rec.RequestHash != reqHash {
					return fmt.Errorf("idempotency_key was already used for a different supersede of task %s", old.ID)
				}
			case errors.Is(err, ErrNotFound):
				rec = nil
			default:
				return err
			}
			replayed := rec != nil
			if rec == nil {
				if a.RetiredAt != nil {
					return denied("agent %s is retired: its session %q was removed", a.ID, a.DevxSession)
				}
				if err := checkPending(old, req.ExpectedRevision, "superseding a prompt"); err != nil {
					return err
				}
				rec = &idemRecord{Kind: "supersede", KeyHash: keyHash, RequestHash: reqHash, AgentID: a.ID, TaskID: newTaskID(),
					SourceTaskID: old.ID, CreatedAt: m.Store.now()}
				if err := m.Store.saveIdem(rec); err != nil {
					return err
				}
			}
			newID := rec.TaskID
			if a.RetiredAt != nil {
				if _, err := m.Store.LoadTask(a.ID, newID); errors.Is(err, ErrNotFound) {
					// Interrupted supersede on an agent retired since: never
					// create new work on a retired agent.
					return denied("agent %s is retired: its session %q was removed", a.ID, a.DevxSession)
				}
			}
			// Step 1: cancel the old task (unless a previous attempt did).
			if old.SupersededBy != newID {
				if err := checkPending(old, req.ExpectedRevision, "superseding a prompt"); err != nil {
					// The record exists but the old task moved on before
					// the supersede was applied (e.g. it was delivered
					// between an interrupted attempt and this retry).
					return err
				}
				now := m.Store.now()
				old.State, old.FinishedAt, old.SupersededBy = TaskCancelled, &now, newID
				old.Note = "superseded by " + newID + " before delivery"
				if err := m.Store.SaveTask(old); err != nil {
					return err
				}
				if _, err := m.Store.AppendEvent(a.ID, Event{Type: "task_cancelled", TaskID: old.ID, Source: "devx",
					Data: map[string]any{"delivered": false, "reason": "superseded", "superseded_by": newID}}); err != nil {
					return err
				}
			}
			// Step 2: create the replacement in the same queue position.
			if _, err := m.Store.LoadTask(a.ID, newID); errors.Is(err, ErrNotFound) {
				nt := &Task{ID: newID, AgentID: a.ID, Order: old.Order, Prompt: req.Prompt, IdempotencyHash: keyHash,
					State: TaskWaiting, CreatedAt: m.Store.now(), Supersedes: old.ID}
				if err := m.Store.SaveTask(nt); err != nil {
					return err
				}
				if _, err := m.Store.AppendEvent(a.ID, Event{Type: "task_queued", TaskID: newID, Source: "devx",
					Data: map[string]any{"order": nt.Order, "prompt_bytes": len(req.Prompt), "supersedes": old.ID}}); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			out = &EditResult{TaskID: old.ID, Revision: old.Revision, Replayed: replayed, ReplacementTaskID: newID}
			return nil
		})
	})
	return out, err
}

// CancelRequest cancels a task. ExpectedRevision (optional) makes the
// cancellation of a pending task compare-and-swap, so a caller does not
// cancel a prompt that someone else just edited.
type CancelRequest struct {
	AgentID          string // optional; when set, the task must belong to it
	TaskID           string
	ExpectedRevision *int64
}

// QueueItem is one entry of the queue view.
type QueueItem struct {
	TaskID        string     `json:"task_id"`
	Order         int64      `json:"order"`
	State         string     `json:"state"`
	Phase         string     `json:"phase"`
	Revision      int64      `json:"revision"`
	WaitingReason string     `json:"waiting_reason,omitempty"`
	CancelOutcome string     `json:"cancel_outcome,omitempty"`
	AgeSeconds    int64      `json:"age_seconds"`
	CreatedAt     time.Time  `json:"created_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	AcceptedAt    *time.Time `json:"accepted_at,omitempty"`
	AcceptedAs    string     `json:"accepted_as,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	PromptBytes   int        `json:"prompt_bytes"`
	PromptPreview string     `json:"prompt_preview"`
	PreviewTrunc  bool       `json:"prompt_preview_truncated,omitempty"`
	Supersedes    string     `json:"supersedes,omitempty"`
	SupersededBy  string     `json:"superseded_by,omitempty"`
	Note          string     `json:"note,omitempty"`
	Editable      bool       `json:"editable"`
}

// QueueView is the queue of one agent: work in flight, pending work in
// delivery order, and a few recent outcomes.
type QueueView struct {
	AgentID  string      `json:"agent_id"`
	State    string      `json:"agent_state"`
	Control  string      `json:"control"`
	LeaseGen int64       `json:"lease_generation"`
	InFlight []QueueItem `json:"in_flight"`
	Pending  []QueueItem `json:"pending"`
	// PendingTotal counts all queued tasks; Pending holds at most
	// MaxQueuePending of them, in delivery order.
	PendingTotal     int         `json:"pending_total"`
	PendingTruncated bool        `json:"pending_truncated,omitempty"`
	Recent           []QueueItem `json:"recent"`
	LastSeq          int64       `json:"last_event_seq"`
}

// MaxQueuePending bounds pending items returned by one queue view.
const MaxQueuePending = 50

// MaxPromptPreview bounds prompt previews in the queue view.
const MaxPromptPreview = 512

// MaxRecent bounds recent finished tasks in the queue view.
const MaxRecent = 10

// Queue returns the reconciled queue for an agent.
func (m *Manager) Queue(agentID string, recent int) (*QueueView, error) {
	if !ValidAgentID(agentID) {
		return nil, fmt.Errorf("invalid agent id %q", agentID)
	}
	av, err := m.AgentStatus(agentID) // reconciles orphaned work first
	if err != nil {
		return nil, err
	}
	tasks, err := m.Store.ListTasks(agentID)
	if err != nil {
		return nil, err
	}
	if recent <= 0 || recent > MaxRecent {
		recent = MaxRecent
	}
	now := m.Store.now()
	qv := &QueueView{AgentID: agentID, State: av.State, Control: av.Agent.Lease.Holder, LeaseGen: av.Agent.Lease.Generation,
		InFlight: []QueueItem{}, Pending: []QueueItem{}, Recent: []QueueItem{}, LastSeq: av.LastSeq}
	var done []QueueItem
	var doneTasks []*Task
	firstWaiting := ""
	for _, t := range tasks {
		if t.State == TaskWaiting {
			firstWaiting = t.ID
			break
		}
	}
	for _, t := range tasks {
		it := QueueItem{TaskID: t.ID, Order: t.Order, State: t.State, Phase: t.Phase(), Revision: t.Revision,
			CancelOutcome: t.CancelOutcome(now), AgeSeconds: int64(now.Sub(t.CreatedAt) / time.Second), CreatedAt: t.CreatedAt,
			DeliveredAt: t.DeliveredAt, AcceptedAt: t.ConfirmedAt, AcceptedAs: t.AcceptedAs, FinishedAt: t.FinishedAt, PromptBytes: len(t.Prompt),
			Supersedes: t.Supersedes, SupersededBy: t.SupersededBy, Note: t.Note, Editable: t.State == TaskWaiting && av.Agent.RetiredAt == nil}
		switch t.State {
		case TaskWaiting:
			qv.PendingTotal++
			if len(qv.Pending) >= MaxQueuePending {
				qv.PendingTruncated = true
				continue
			}
			it.WaitingReason = waitingReasonFor(av, t, firstWaiting)
			qv.Pending = append(qv.Pending, withPreview(it, t))
		case TaskRunning:
			qv.InFlight = append(qv.InFlight, withPreview(it, t))
		default:
			done = append(done, it)
			doneTasks = append(doneTasks, t)
		}
	}
	if n := len(done); n > recent {
		done, doneTasks = done[n-recent:], doneTasks[n-recent:]
	}
	for i := range done {
		qv.Recent = append(qv.Recent, withPreview(done[i], doneTasks[i]))
	}
	return qv, nil
}

// withPreview adds the bounded preview. It redacts before cutting, so a
// credential straddling the cut is still recognised, and is only computed
// for items actually returned.
func withPreview(it QueueItem, t *Task) QueueItem {
	it.PromptPreview, it.PreviewTrunc = headExcerpt(Redact(t.Prompt), MaxPromptPreview)
	return it
}
