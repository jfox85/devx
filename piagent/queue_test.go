package piagent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// Unit tests for pending-work controls (no Pi, no tmux). Bridge behavior is
// simulated by writing task state under the agent lock exactly as the
// bridge does; real-Pi behavior is in takeover_e2e_test.go.

func sendT(t *testing.T, m *Manager, agentID, prompt, key string) *Task {
	t.Helper()
	r, err := m.Send(SendRequest{AgentID: agentID, Prompt: prompt, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := m.Store.LoadTask(agentID, r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// bridgeWrite mutates a task the way the bridge does: under the agent lock,
// bumping the revision.
func bridgeWrite(t *testing.T, m *Manager, agentID, taskID string, fn func(*Task)) *Task {
	t.Helper()
	var out *Task
	if err := m.Store.WithAgentLock(agentID, func() error {
		tk, err := m.Store.LoadTask(agentID, taskID)
		if err != nil {
			return err
		}
		fn(tk)
		out = tk
		return m.Store.SaveTask(tk)
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func claim(tk *Task) {
	now := time.Now()
	tk.State, tk.DeliveredAt, tk.BridgeInstance = TaskRunning, &now, "inst"
}

func accept(tk *Task) {
	now := time.Now()
	tk.ConfirmedAt = &now
}

func isConflict(err error) (*ConflictError, bool) {
	var ce *ConflictError
	ok := errors.As(err, &ce)
	return ce, ok
}

func TestPhasesSeparateQueuedDeliveredAccepted(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "p", "k")
	if tk.Phase() != PhaseQueued || tk.Revision != 1 {
		t.Fatalf("queued: phase=%s rev=%d", tk.Phase(), tk.Revision)
	}
	tk = bridgeWrite(t, m, a.ID, tk.ID, claim)
	if tk.Phase() != PhaseDelivered {
		t.Fatalf("claimed: %s", tk.Phase())
	}
	tk = bridgeWrite(t, m, a.ID, tk.ID, accept)
	if tk.Phase() != PhaseAccepted {
		t.Fatalf("accepted: %s", tk.Phase())
	}
	tv, _, err := m.TaskStatus(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tv.Phase != PhaseAccepted || tv.AcceptedAt == nil || tv.Revision != tk.Revision {
		t.Fatalf("view: %+v", tv)
	}
}

func TestUpdatePendingCASAndIdempotency(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "old prompt", "k")
	r, err := m.UpdatePending(UpdatePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "new prompt", ExpectedRevision: tk.Revision, IdempotencyKey: "e1"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != tk.Revision+1 || r.Replayed {
		t.Fatalf("%+v", r)
	}
	// Retry after a dropped response: same result, not applied twice.
	r2, err := m.UpdatePending(UpdatePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "new prompt", ExpectedRevision: tk.Revision, IdempotencyKey: "e1"})
	if err != nil || !r2.Replayed || r2.Revision != r.Revision {
		t.Fatalf("replay: %+v %v", r2, err)
	}
	// Same key, different request: error, nothing changes.
	if _, err := m.UpdatePending(UpdatePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "other", ExpectedRevision: r.Revision, IdempotencyKey: "e1"}); err == nil {
		t.Fatal("key reuse must fail")
	}
	// Stale revision: conflict with the current revision; nothing changes.
	_, err = m.UpdatePending(UpdatePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "stale", ExpectedRevision: tk.Revision, IdempotencyKey: "e2"})
	ce, ok := isConflict(err)
	if !ok || ce.Current != r.Revision || ce.State != TaskWaiting {
		t.Fatalf("want conflict, got %v", err)
	}
	got, _ := m.Store.LoadTask(a.ID, tk.ID)
	if got.Prompt != "new prompt" || got.Order != tk.Order || got.ID != tk.ID {
		t.Fatalf("task after edits: %+v", got)
	}
	evs, _, _ := m.Store.ReadEvents(a.ID, 0, 100)
	if n := countType(evs, "task_updated"); n != 1 {
		t.Fatalf("task_updated events=%d", n)
	}
}

func countType(evs []Event, typ string) int {
	n := 0
	for _, e := range evs {
		if e.Type == typ {
			n++
		}
	}
	return n
}

// A prompt handed to Pi can never be edited or superseded, even with the
// revision the caller read just before the claim.
func TestEditsNeverTouchDeliveredWork(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "p", "k")
	read := tk.Revision
	bridgeWrite(t, m, a.ID, tk.ID, claim) // the bridge wins the race
	_, err := m.UpdatePending(UpdatePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "edit", ExpectedRevision: read, IdempotencyKey: "e"})
	if ce, ok := isConflict(err); !ok || ce.Phase != PhaseDelivered {
		t.Fatalf("update after delivery: %v", err)
	}
	_, err = m.SupersedePending(SupersedePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "edit", ExpectedRevision: read, IdempotencyKey: "s"})
	if _, ok := isConflict(err); !ok {
		t.Fatalf("supersede after delivery: %v", err)
	}
	got, _ := m.Store.LoadTask(a.ID, tk.ID)
	if got.Prompt != "p" || got.State != TaskRunning {
		t.Fatalf("delivered task changed: %+v", got)
	}
	tasks, _ := m.Store.ListTasks(a.ID)
	if len(tasks) != 1 {
		t.Fatalf("supersede created a task: %d", len(tasks))
	}
}

func TestSupersedeKeepsPositionAndIsIdempotent(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	first := sendT(t, m, a.ID, "first", "k1")
	second := sendT(t, m, a.ID, "second", "k2")
	third := sendT(t, m, a.ID, "third", "k3")
	req := SupersedePendingRequest{AgentID: a.ID, TaskID: second.ID, Prompt: "second v2", ExpectedRevision: second.Revision, IdempotencyKey: "s"}
	r, err := m.SupersedePending(req)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := m.SupersedePending(req)
	if err != nil || !r2.Replayed || r2.ReplacementTaskID != r.ReplacementTaskID {
		t.Fatalf("replay: %+v %v", r2, err)
	}
	q, err := m.Queue(a.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, it := range q.Pending {
		order = append(order, it.TaskID)
	}
	if len(order) != 3 || order[0] != first.ID || order[1] != r.ReplacementTaskID || order[2] != third.ID {
		t.Fatalf("pending order %v", order)
	}
	old, _ := m.Store.LoadTask(a.ID, second.ID)
	if old.State != TaskCancelled || old.SupersededBy != r.ReplacementTaskID || old.CancelOutcome(time.Now()) != CancelSuperseded {
		t.Fatalf("old: %+v", old)
	}
	nt, _ := m.Store.LoadTask(a.ID, r.ReplacementTaskID)
	if nt.Supersedes != second.ID || nt.Prompt != "second v2" || nt.State != TaskWaiting {
		t.Fatalf("new: %+v", nt)
	}
	// The superseded original shows in recent outcomes, not in pending.
	if q.Recent[0].TaskID != second.ID || q.Recent[0].CancelOutcome != CancelSuperseded {
		t.Fatalf("recent: %+v", q.Recent)
	}
}

// supersedeRecord writes the idempotency record a supersede writes first,
// simulating a crash right after it.
func supersedeRecord(t *testing.T, m *Manager, req SupersedePendingRequest) *idemRecord {
	t.Helper()
	keyHash := hashKey("supersede", req.AgentID, req.TaskID, req.IdempotencyKey)
	rec := &idemRecord{Kind: "supersede", KeyHash: keyHash, RequestHash: hashKey(req.Prompt, fmt.Sprint(req.ExpectedRevision)),
		AgentID: req.AgentID, TaskID: newTaskID(), SourceTaskID: req.TaskID, CreatedAt: time.Now()}
	if err := m.Store.saveIdem(rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func waitingIDs(t *testing.T, m *Manager, agentID string) []string {
	t.Helper()
	tasks, err := m.Store.ListTasks(agentID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, x := range tasks {
		if x.State == TaskWaiting {
			out = append(out, x.ID)
		}
	}
	return out
}

// A supersede interrupted at any point is completed by the retry with the
// same replacement id; it never leaves two waiting copies, and never loses
// the replacement.
func TestSupersedeCrashRecovery(t *testing.T) {
	t.Run("after_record", func(t *testing.T) {
		m, _ := newTestManager(t)
		a := seedAgent(t, m)
		tk := sendT(t, m, a.ID, "p", "k")
		req := SupersedePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "p2", ExpectedRevision: tk.Revision, IdempotencyKey: "s"}
		rec := supersedeRecord(t, m, req)
		r, err := m.SupersedePending(req)
		if err != nil || r.ReplacementTaskID != rec.TaskID {
			t.Fatalf("retry: %+v %v (want replacement %s)", r, err, rec.TaskID)
		}
		if w := waitingIDs(t, m, a.ID); len(w) != 1 || w[0] != rec.TaskID {
			t.Fatalf("waiting=%v", w)
		}
	})
	t.Run("after_old_cancelled", func(t *testing.T) {
		m, _ := newTestManager(t)
		a := seedAgent(t, m)
		tk := sendT(t, m, a.ID, "p", "k")
		req := SupersedePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "p2", ExpectedRevision: tk.Revision, IdempotencyKey: "s"}
		rec := supersedeRecord(t, m, req)
		// Crash after step 1: old cancelled with superseded_by, no replacement.
		bridgeWrite(t, m, a.ID, tk.ID, func(x *Task) {
			now := time.Now()
			x.State, x.FinishedAt, x.SupersededBy = TaskCancelled, &now, rec.TaskID
		})
		if w := waitingIDs(t, m, a.ID); len(w) != 0 {
			t.Fatalf("precondition: %v", w)
		}
		r, err := m.SupersedePending(req)
		if err != nil || r.ReplacementTaskID != rec.TaskID || !r.Replayed {
			t.Fatalf("retry: %+v %v", r, err)
		}
		if w := waitingIDs(t, m, a.ID); len(w) != 1 || w[0] != rec.TaskID {
			t.Fatalf("waiting=%v", w)
		}
		nt, _ := m.Store.LoadTask(a.ID, rec.TaskID)
		if nt.Prompt != "p2" || nt.Order != tk.Order || nt.Supersedes != tk.ID {
			t.Fatalf("replacement: %+v", nt)
		}
	})
	t.Run("old_claimed_before_retry", func(t *testing.T) {
		m, _ := newTestManager(t)
		a := seedAgent(t, m)
		tk := sendT(t, m, a.ID, "p", "k")
		req := SupersedePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "p2", ExpectedRevision: tk.Revision, IdempotencyKey: "s"}
		rec := supersedeRecord(t, m, req)
		bridgeWrite(t, m, a.ID, tk.ID, claim) // the bridge delivered it meanwhile
		if _, err := m.SupersedePending(req); err == nil {
			t.Fatal("want conflict")
		} else if _, ok := isConflict(err); !ok {
			t.Fatalf("want conflict, got %v", err)
		}
		if _, err := m.Store.LoadTask(a.ID, rec.TaskID); !errors.Is(err, ErrNotFound) {
			t.Fatal("replacement created although the original was delivered")
		}
	})
}

// A->B->A: the bridge claims a task and fences it back to waiting. The
// state is waiting again, but the revision moved, so an edit made with the
// revision read before the claim must conflict.
func TestStaleRevisionAfterClaimAndRequeueConflicts(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "p", "k")
	read := tk.Revision
	bridgeWrite(t, m, a.ID, tk.ID, claim)
	back := bridgeWrite(t, m, a.ID, tk.ID, func(x *Task) { x.State, x.DeliveredAt, x.BridgeInstance = TaskWaiting, nil, "" })
	if back.State != TaskWaiting || back.Revision <= read {
		t.Fatalf("precondition: %+v", back)
	}
	_, err := m.UpdatePending(UpdatePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "stale", ExpectedRevision: read, IdempotencyKey: "e"})
	if ce, ok := isConflict(err); !ok || ce.Current != back.Revision {
		t.Fatalf("want conflict at %d, got %v", back.Revision, err)
	}
}

// An interrupted supersede is never completed on an agent retired since:
// no new work on a retired agent.
func TestSupersedeReplayOnRetiredAgentCreatesNothing(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "p", "k")
	keyHash := hashKey("supersede", a.ID, tk.ID, "s")
	pre := &idemRecord{Kind: "supersede", KeyHash: keyHash, RequestHash: hashKey("p2", "1"), AgentID: a.ID, TaskID: newTaskID(), SourceTaskID: tk.ID, CreatedAt: time.Now()}
	if err := m.Store.saveIdem(pre); err != nil {
		t.Fatal(err)
	}
	if err := m.RetireForSession(a.ID, a.DevxSession); err != nil {
		t.Fatal(err)
	}
	_, err := m.SupersedePending(SupersedePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "p2", ExpectedRevision: tk.Revision, IdempotencyKey: "s"})
	if !IsPermissionDenied(err) {
		t.Fatalf("want denied, got %v", err)
	}
	if _, err := m.Store.LoadTask(a.ID, pre.TaskID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replacement created on retired agent: %v", err)
	}
}

// Concurrent edits from two clients that read the same revision: exactly
// one wins; the other gets a conflict and its text is not written.
func TestConcurrentQueueEditsOneWins(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "base", "k")
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				_, errs[i] = m.UpdatePending(UpdatePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "edit", ExpectedRevision: tk.Revision, IdempotencyKey: "u" + string(rune('a'+i))})
			} else {
				_, errs[i] = m.SupersedePending(SupersedePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "sup", ExpectedRevision: tk.Revision, IdempotencyKey: "s" + string(rune('a'+i))})
			}
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if _, c := isConflict(err); !c {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("winners=%d, want 1", ok)
	}
	tasks, _ := m.Store.ListTasks(a.ID)
	waiting := 0
	for _, x := range tasks {
		if x.State == TaskWaiting {
			waiting++
		}
	}
	if waiting != 1 {
		t.Fatalf("waiting copies=%d", waiting)
	}
	// The single waiting copy carries exactly the winner's text.
	for _, x := range tasks {
		if x.State == TaskWaiting && x.Prompt != "edit" && x.Prompt != "sup" {
			t.Fatalf("waiting prompt %q", x.Prompt)
		}
	}
}

// Identity: an edit or cancel naming the wrong agent never touches another
// agent's task; a retired agent accepts no edits.
func TestStaleIdentityIsRejected(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	b := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "p", "k")
	if _, err := m.UpdatePending(UpdatePendingRequest{AgentID: b.ID, TaskID: tk.ID, Prompt: "x", ExpectedRevision: tk.Revision, IdempotencyKey: "e"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong agent update: %v", err)
	}
	if _, err := m.CancelTask(CancelRequest{AgentID: b.ID, TaskID: tk.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong agent cancel: %v", err)
	}
	if _, err := m.UpdatePending(UpdatePendingRequest{AgentID: "pa_zz", TaskID: tk.ID, Prompt: "x", ExpectedRevision: 1, IdempotencyKey: "e"}); err == nil {
		t.Fatal("invalid agent id accepted")
	}
	_ = m.Store.WithAgentLock(a.ID, func() error {
		ag, _ := m.Store.LoadAgent(a.ID)
		now := time.Now()
		ag.RetiredAt = &now
		return m.Store.SaveAgent(ag)
	})
	if _, err := m.UpdatePending(UpdatePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "x", ExpectedRevision: tk.Revision, IdempotencyKey: "e"}); !IsPermissionDenied(err) {
		t.Fatalf("retired agent update: %v", err)
	}
	if _, err := m.SupersedePending(SupersedePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "x", ExpectedRevision: tk.Revision, IdempotencyKey: "s"}); !IsPermissionDenied(err) {
		t.Fatalf("retired agent supersede: %v", err)
	}
	if q, err := m.Queue(a.ID, 0); err != nil || len(q.Pending) != 1 || q.Pending[0].Editable {
		t.Fatalf("retired queue view: %+v %v", q, err)
	}
	got, _ := m.Store.LoadTask(a.ID, tk.ID)
	if got.Prompt != "p" {
		t.Fatal("task changed")
	}
}

// Cancel of a task handed to Pi but not accepted is allowed under human
// control (it only drops the injection); an accepted turn is denied.
func TestCancelUnacceptedAllowedUnderHumanControl(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "p", "k")
	bridgeWrite(t, m, a.ID, tk.ID, claim)
	if _, err := m.Takeover(a.ID, "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel(tk.ID); err != nil {
		t.Fatalf("unaccepted cancel under human control: %v", err)
	}
	if got, _ := m.Store.LoadTask(a.ID, tk.ID); !got.CancelRequested {
		t.Fatal("not recorded")
	}
}

// A waiting task carrying a stale cancel flag (an older bridge put a
// cancelled hand-off back) is still cancelled, not treated as done.
func TestWaitingTaskWithStaleCancelFlagIsCancelled(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "p", "k")
	bridgeWrite(t, m, a.ID, tk.ID, func(x *Task) { x.CancelRequested = true })
	if _, err := m.Cancel(tk.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Store.LoadTask(a.ID, tk.ID); got.State != TaskCancelled || got.CancelOutcome(time.Now()) != CancelBeforeDelivery {
		t.Fatalf("%+v", got)
	}
}

// A cancelled turn whose Pi process went away reports outcome_unknown, not
// finished_before_abort.
func TestCancelThenPiGoneIsOutcomeUnknown(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "p", "k")
	bridgeWrite(t, m, a.ID, tk.ID, func(x *Task) { claim(x); accept(x) })
	if _, err := m.Cancel(tk.ID); err != nil {
		t.Fatal(err)
	}
	got := bridgeWrite(t, m, a.ID, tk.ID, func(x *Task) {
		now := time.Now()
		x.AbortSentAt = &now
		x.State, x.FinishedAt = TaskUnknown, &now
	})
	if o := got.CancelOutcome(time.Now()); o != CancelOutcomeUnknown {
		t.Fatalf("outcome=%s", o)
	}
}

func TestCancelOutcomesAndHumanOwnership(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)

	// Waiting: cancelled before delivery, revision-checked.
	w := sendT(t, m, a.ID, "w", "kw")
	bad := w.Revision + 5
	if _, err := m.CancelTask(CancelRequest{TaskID: w.ID, ExpectedRevision: &bad}); err == nil {
		t.Fatal("stale revision cancel must conflict")
	}
	if _, err := m.CancelTask(CancelRequest{AgentID: a.ID, TaskID: w.ID, ExpectedRevision: &w.Revision}); err != nil {
		t.Fatal(err)
	}
	tv, _, _ := m.TaskStatus(w.ID)
	if tv.State != TaskCancelled || tv.CancelOutcome != CancelBeforeDelivery {
		t.Fatalf("waiting cancel: %+v", tv)
	}
	// Waiting cancel is also allowed under human control (queued work is not
	// running work).
	w2 := sendT(t, m, a.ID, "w2", "kw2")
	if _, err := m.Takeover(a.ID, "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel(w2.ID); err != nil {
		t.Fatalf("waiting cancel under human control: %v", err)
	}
	if _, err := m.Release(a.ID, "h", false); err != nil {
		t.Fatal(err)
	}

	// Delivered but not accepted: cancel requested; outcome resolved by the
	// bridge's input handler (simulated).
	d := sendT(t, m, a.ID, "d", "kd")
	bridgeWrite(t, m, a.ID, d.ID, claim)
	if _, err := m.Cancel(d.ID); err != nil {
		t.Fatal(err)
	}
	bridgeWrite(t, m, a.ID, d.ID, func(tk *Task) {
		now := time.Now()
		tk.State, tk.FinishedAt = TaskCancelled, &now
	})
	if tv, _, _ := m.TaskStatus(d.ID); tv.CancelOutcome != CancelBeforeAcceptance {
		t.Fatalf("delivered cancel: %+v", tv)
	}

	// Accepted (running): pending -> sent -> unconfirmed -> aborted.
	r := sendT(t, m, a.ID, "r", "kr")
	bridgeWrite(t, m, a.ID, r.ID, func(tk *Task) { claim(tk); accept(tk) })
	if _, err := m.Cancel(r.ID); err != nil {
		t.Fatal(err)
	}
	tk, _ := m.Store.LoadTask(a.ID, r.ID)
	ag, _ := m.Store.LoadAgent(a.ID)
	if tk.CancelLeaseGeneration != ag.Lease.Generation || tk.CancelOutcome(time.Now()) != CancelAbortPending {
		t.Fatalf("pending: %+v", tk)
	}
	rev := tk.Revision
	if _, err := m.Cancel(r.ID); err != nil { // repeat: no-op
		t.Fatal(err)
	}
	if tk, _ = m.Store.LoadTask(a.ID, r.ID); tk.Revision != rev {
		t.Fatal("repeat cancel rewrote the task")
	}
	sent := time.Now().Add(-AbortConfirmAfter - time.Second)
	tk = bridgeWrite(t, m, a.ID, r.ID, func(tk *Task) { tk.AbortSentAt = &sent })
	if tk.CancelOutcome(time.Now()) != CancelAbortUnconfirmed || tk.CancelOutcome(sent.Add(time.Second)) != CancelAbortSent {
		t.Fatalf("sent/unconfirmed: %s", tk.CancelOutcome(time.Now()))
	}
	tk = bridgeWrite(t, m, a.ID, r.ID, func(tk *Task) {
		now := time.Now()
		tk.State, tk.FinishedAt, tk.StopReason = TaskCancelled, &now, "aborted"
	})
	if tk.CancelOutcome(time.Now()) != CancelAborted {
		t.Fatalf("aborted: %s", tk.CancelOutcome(time.Now()))
	}

	// Running under human control: denied, nothing recorded.
	h := sendT(t, m, a.ID, "h", "kh")
	bridgeWrite(t, m, a.ID, h.ID, func(tk *Task) { claim(tk); accept(tk) })
	if _, err := m.Takeover(a.ID, "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel(h.ID); !IsPermissionDenied(err) {
		t.Fatalf("want denied, got %v", err)
	}
	if tk, _ := m.Store.LoadTask(a.ID, h.ID); tk.CancelRequested {
		t.Fatal("denied cancel was recorded")
	}

	// Finished before the abort landed.
	if _, err := m.Release(a.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel(h.ID); err != nil {
		t.Fatal(err)
	}
	tk = bridgeWrite(t, m, a.ID, h.ID, func(tk *Task) {
		now := time.Now()
		tk.State, tk.FinishedAt = TaskCompleted, &now
	})
	if tk.CancelOutcome(time.Now()) != CancelFinishedFirst {
		t.Fatalf("finished first: %s", tk.CancelOutcome(time.Now()))
	}
	// Withheld (control changed between request and bridge action).
	x := sendT(t, m, a.ID, "x", "kx")
	bridgeWrite(t, m, a.ID, x.ID, func(tk *Task) { claim(tk); accept(tk) })
	if _, err := m.Cancel(x.ID); err != nil {
		t.Fatal(err)
	}
	tk = bridgeWrite(t, m, a.ID, x.ID, func(tk *Task) { tk.AbortWithheld = "human_control" })
	if tk.CancelOutcome(time.Now()) != CancelWithheld {
		t.Fatalf("withheld: %s", tk.CancelOutcome(time.Now()))
	}
	// Under human control a withheld cancel cannot be re-armed remotely.
	if _, err := m.Takeover(a.ID, "human"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel(x.ID); err != nil { // no-op: still withheld
		t.Fatal(err)
	}
	if tk, _ := m.Store.LoadTask(a.ID, x.ID); tk.AbortWithheld == "" {
		t.Fatal("re-armed under human control")
	}
	// After the human releases, a new cancel request re-arms it at the new
	// lease generation.
	if _, err := m.Release(a.ID, "human", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel(x.ID); err != nil {
		t.Fatal(err)
	}
	tk, _ = m.Store.LoadTask(a.ID, x.ID)
	ag, _ = m.Store.LoadAgent(a.ID)
	if tk.AbortWithheld != "" || tk.CancelLeaseGeneration != ag.Lease.Generation || tk.CancelOutcome(time.Now()) != CancelAbortPending {
		t.Fatalf("re-arm: %+v", tk)
	}
}

// State and revisions live on disk: a fresh manager (MCP server restart)
// over the same store sees the same revisions and recognises replays.
func TestQueueStateSurvivesRestart(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	tk := sendT(t, m, a.ID, "p", "k")
	req := UpdatePendingRequest{AgentID: a.ID, TaskID: tk.ID, Prompt: "p2", ExpectedRevision: tk.Revision, IdempotencyKey: "e"}
	r1, err := m.UpdatePending(req)
	if err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(NewStore(m.Store.Root), m.Tmux, m.Creator, m.Config)
	r2, err := m2.UpdatePending(req)
	if err != nil || !r2.Replayed || r2.Revision != r1.Revision {
		t.Fatalf("after restart: %+v %v", r2, err)
	}
	q, err := m2.Queue(a.ID, 0)
	if err != nil || len(q.Pending) != 1 || q.Pending[0].Revision != r1.Revision || !q.Pending[0].Editable {
		t.Fatalf("queue after restart: %+v %v", q, err)
	}
}

// The queue view is bounded and redacted, and reports why work waits.
func TestQueueViewBoundedAndRedacted(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	big := "OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz0123456789 " + strings.Repeat("x", 4000)
	tk := sendT(t, m, a.ID, big, "k")
	q, err := m.Queue(a.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	it := q.Pending[0]
	if it.TaskID != tk.ID || it.PromptBytes != len(big) || !it.PreviewTrunc || len(it.PromptPreview) > MaxPromptPreview+64 {
		t.Fatalf("item: bytes=%d trunc=%v len=%d", it.PromptBytes, it.PreviewTrunc, len(it.PromptPreview))
	}
	if strings.Contains(it.PromptPreview, "sk-proj-abcdefghijklmnop") {
		t.Fatal("preview not redacted")
	}
	if it.WaitingReason == "" || it.Phase != PhaseQueued {
		t.Fatalf("reason/phase: %+v", it)
	}
	// A credential straddling the preview cut is still redacted.
	straddle := strings.Repeat("y", MaxPromptPreview-12) + " sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"
	sendT(t, m, a.ID, straddle, "k2")
	q, _ = m.Queue(a.ID, 0)
	if p := q.Pending[1].PromptPreview; strings.Contains(p, "sk-proj-abc") {
		t.Fatalf("partial key leaked at the cut: %q", p[len(p)-40:])
	}
}

func TestQueueViewCapsPending(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	var first string
	for i := 0; i < MaxQueuePending+5; i++ {
		tk := sendT(t, m, a.ID, "p", "k"+strings.Repeat("x", i))
		if i == 0 {
			first = tk.ID
		}
	}
	q, err := m.Queue(a.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Pending) != MaxQueuePending || q.PendingTotal != MaxQueuePending+5 || !q.PendingTruncated || q.Pending[0].TaskID != first {
		t.Fatalf("pending=%d total=%d trunc=%v", len(q.Pending), q.PendingTotal, q.PendingTruncated)
	}
	if q.Pending[1].WaitingReason != WaitBridgeOnline {
		// no bridge in unit tests: offline dominates queue position
		t.Fatalf("reason=%s", q.Pending[1].WaitingReason)
	}
}
