//go:build !windows

package piagent

import (
	"strings"
	"testing"
	"time"
)

// Real interactive Pi (faux model) tests for pending-work controls and the
// running-cancel outcome. Same fixture as takeover_e2e_test.go.

// Busy agent: a follow-up queued during a running turn stays in the DevX
// queue (it is never handed to Pi as a steer), can be edited, and is then
// delivered as its own turn after the first one finishes.
func TestE2EQueuedFollowUpIsEditableWhileBusyAndNeverSteers(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("first SLOW", "k1")
	f.waitTask(r.TaskID, 30*time.Second, TaskRunning)
	follow := f.send(r.AgentID, "original follow-up", "k2")
	time.Sleep(800 * time.Millisecond) // many bridge ticks while busy
	tv, _, _ := f.m.TaskStatus(follow)
	if tv.Phase != PhaseQueued || tv.WaitingReason != WaitQueued {
		t.Fatalf("follow-up while busy: phase=%s reason=%s", tv.Phase, tv.WaitingReason)
	}
	if _, err := f.m.UpdatePending(UpdatePendingRequest{AgentID: r.AgentID, TaskID: follow, Prompt: "edited follow-up", ExpectedRevision: tv.Revision, IdempotencyKey: "e1"}); err != nil {
		t.Fatal(err)
	}
	first := f.waitTask(r.TaskID, 60*time.Second, TaskCompleted, TaskFailed)
	if first.State != TaskCompleted || strings.Contains(first.Excerpt, "follow-up") {
		t.Fatalf("first turn was steered or failed: %+v", first)
	}
	done := f.waitTask(follow, 45*time.Second, TaskCompleted, TaskFailed)
	if done.State != TaskCompleted || !strings.Contains(done.Excerpt, "ECHO: edited follow-up") || done.AcceptedAt == nil {
		t.Fatalf("follow-up: %+v", done)
	}
	if strings.Contains(f.pane(r.AgentID), "original follow-up") {
		t.Fatalf("pre-edit prompt reached Pi:\n%s", f.pane(r.AgentID))
	}
}

// Running cancel: outcome is observable (abort_sent then aborted), the
// lease stays managed, and the human's editor is not touched.
func TestE2ERunningCancelOutcome(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("long SLOW", "k1")
	f.waitTask(r.TaskID, 30*time.Second, TaskRunning)
	f.waitAgent(r.AgentID, 10*time.Second, func(v *AgentView) bool {
		tv, _, _ := f.m.TaskStatus(r.TaskID)
		return tv.Phase == PhaseAccepted
	}, "accepted")
	time.Sleep(300 * time.Millisecond)
	if _, err := f.m.Cancel(r.TaskID); err != nil {
		t.Fatal(err)
	}
	tv := f.waitTask(r.TaskID, 30*time.Second, TaskCancelled, TaskCompleted, TaskFailed)
	if tv.State != TaskCancelled || tv.CancelOutcome != CancelAborted || tv.AbortSentAt == nil {
		t.Fatalf("running cancel: %+v\nevents=%s", tv, eventTypes(f.events(r.AgentID)))
	}
	// A second cancel after the abort is a no-op: still exactly one abort.
	if _, err := f.m.Cancel(r.TaskID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	types := eventTypes(f.events(r.AgentID))
	ci, ai := strings.Index(types, "cancel_requested"), strings.Index(types, "abort_sent")
	if ci < 0 || ai < 0 || ci > ai {
		t.Fatalf("want cancel_requested before abort_sent: %s", types)
	}
	if strings.Count(types, "abort_sent") != 1 || strings.Count(types, "cancel_requested") != 1 {
		t.Fatalf("abort/cancel recorded more than once: %s", types)
	}
	v, _ := f.m.AgentStatus(r.AgentID)
	if v.Agent.Lease.Holder != LeaseManaged {
		t.Fatalf("lease moved to %s", v.Agent.Lease.Holder)
	}
	// The agent remains usable for the next queued prompt.
	next := f.send(r.AgentID, "after abort", "k2")
	if tv := f.waitTask(next, 45*time.Second, TaskCompleted, TaskFailed); tv.State != TaskCompleted {
		t.Fatalf("next after abort: %+v", tv)
	}
}

// editorContains reports whether any visible pane line holds text.
func (f *piFixture) editorContains(agentID, text string) bool {
	for _, l := range strings.Split(f.pane(agentID), "\n") {
		if strings.Contains(l, text) {
			return true
		}
	}
	return false
}

// A human has typed (not submitted) a draft while DevX still holds control,
// then a remote cancel aborts the running turn: the abort happens, and the
// draft is still in the editor afterwards. (Pi moves only its OWN queued
// messages into the editor on abort; the bridge refuses to abort while Pi
// has any, so the draft is never mixed with anything.)
func TestE2EManagedAbortPreservesUnsentHumanDraft(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("long SLOW", "k1")
	f.waitTask(r.TaskID, 30*time.Second, TaskRunning)
	f.waitAgent(r.AgentID, 10*time.Second, func(v *AgentView) bool {
		tv, _, _ := f.m.TaskStatus(r.TaskID)
		return tv.Phase == PhaseAccepted
	}, "accepted")
	const draft = "unsent-draft-QX7"
	f.humanType(r.AgentID, draft, false)
	f.waitAgent(r.AgentID, 10*time.Second, func(v *AgentView) bool { return v.Bridge != nil && v.Bridge.HumanTyping }, "draft present")
	if v, _ := f.m.AgentStatus(r.AgentID); v.Agent.Lease.Holder != LeaseManaged {
		t.Fatalf("typing without submit moved the lease: %s", v.Agent.Lease.Holder)
	}
	if _, err := f.m.Cancel(r.TaskID); err != nil {
		t.Fatal(err)
	}
	tv := f.waitTask(r.TaskID, 30*time.Second, TaskCancelled, TaskCompleted, TaskFailed)
	if tv.CancelOutcome != CancelAborted {
		t.Fatalf("outcome: %+v", tv)
	}
	time.Sleep(500 * time.Millisecond)
	v := f.waitAgent(r.AgentID, 5*time.Second, func(v *AgentView) bool { return v.Bridge != nil }, "bridge")
	if !v.Bridge.HumanTyping || !f.editorContains(r.AgentID, draft) {
		t.Fatalf("draft lost after abort (typing=%v):\n%s", v.Bridge.HumanTyping, f.pane(r.AgentID))
	}
	// Nothing was submitted on the human's behalf.
	if strings.Contains(eventTypes(f.events(r.AgentID)), "human_input") {
		t.Fatalf("draft was submitted: %s", eventTypes(f.events(r.AgentID)))
	}
}

// Control changed and came back (takeover + release) between the cancel
// request and the bridge acting: the lease is managed again but at a newer
// generation, so the abort is withheld rather than executed on a request
// made under different control.
func TestE2EAbortWithheldWhenLeaseGenerationChanged(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("long SLOW", "k1")
	f.waitTask(r.TaskID, 30*time.Second, TaskRunning)
	f.waitAgent(r.AgentID, 10*time.Second, func(v *AgentView) bool {
		tv, _, _ := f.m.TaskStatus(r.TaskID)
		return tv.Phase == PhaseAccepted
	}, "accepted")
	if err := f.m.Store.WithAgentLock(r.AgentID, func() error {
		a, err := f.m.Store.LoadAgent(r.AgentID)
		if err != nil {
			return err
		}
		tk, err := f.m.Store.LoadTask(r.AgentID, r.TaskID)
		if err != nil {
			return err
		}
		now := f.m.Store.now()
		tk.CancelRequested, tk.CancelRequestedAt, tk.CancelLeaseGeneration = true, &now, a.Lease.Generation
		if err := f.m.Store.SaveTask(tk); err != nil {
			return err
		}
		a.Lease = Lease{Holder: LeaseManaged, Generation: a.Lease.Generation + 2, Since: now, Reason: "explicit_release", By: "test"}
		return f.m.Store.SaveAgent(a)
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		tv, _, _ := f.m.TaskStatus(r.TaskID)
		if tv.CancelOutcome == CancelWithheld {
			if tv.AbortWithheld != "control_changed_since_request" || tv.AbortSentAt != nil {
				t.Fatalf("%+v", tv)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not withheld: %+v events=%s", tv, eventTypes(f.events(r.AgentID)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	// A fresh request under the current generation is honoured.
	if _, err := f.m.Cancel(r.TaskID); err != nil {
		t.Fatal(err)
	}
	if tv := f.waitTask(r.TaskID, 30*time.Second, TaskCancelled, TaskCompleted); tv.CancelOutcome != CancelAborted {
		t.Fatalf("re-armed cancel: %+v", tv)
	}
}

// The real bridge bumps the revision on every write: claim, accept and
// finish each advance it, and an edit with a revision read before the
// claim conflicts while the task is in the delivery window.
func TestE2ERevisionAdvancesAcrossRealBridgePhases(t *testing.T) {
	f := newPiFixture(t, "DEVX_PI_TEST_DELIVERY_DELAY_MS=3000")
	r := f.start("warmup", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	if err := f.m.Store.WithAgentLock(r.AgentID, func() error { // pause delivery
		a, _ := f.m.Store.LoadAgent(r.AgentID)
		a.Lease = Lease{Holder: LeaseHuman, Generation: a.Lease.Generation + 1, Reason: "test"}
		return f.m.Store.SaveAgent(a)
	}); err != nil {
		t.Fatal(err)
	}
	task := f.send(r.AgentID, "rev probe", "k2")
	tv0, _, _ := f.m.TaskStatus(task)
	if _, err := f.m.Release(r.AgentID, "test", false); err != nil {
		t.Fatal(err)
	}
	tv1 := f.waitTask(task, 15*time.Second, TaskRunning) // claimed, inside the delay
	if tv1.Phase != PhaseDelivered || tv1.Revision <= tv0.Revision {
		t.Fatalf("claim: %+v (before %d)", tv1, tv0.Revision)
	}
	_, err := f.m.UpdatePending(UpdatePendingRequest{AgentID: r.AgentID, TaskID: task, Prompt: "too late", ExpectedRevision: tv0.Revision, IdempotencyKey: "late"})
	if ce, ok := isConflict(err); !ok || ce.Phase != PhaseDelivered {
		t.Fatalf("edit in delivery window: %v", err)
	}
	tv2 := f.waitTask(task, 45*time.Second, TaskCompleted, TaskFailed)
	if tv2.State != TaskCompleted || tv2.Revision < tv1.Revision+2 || tv2.AcceptedAs != "turn" {
		t.Fatalf("finish: %+v (claim rev %d)", tv2, tv1.Revision)
	}
	if strings.Contains(tv2.Excerpt, "too late") {
		t.Fatal("late edit reached Pi")
	}
}

// Several queued follow-ups while busy: edited/cancelled ones are honoured,
// the rest are delivered one at a time in queue order, each as its own turn.
func TestE2EMultipleQueuedFollowUpsInOrder(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("first SLOW", "k1")
	f.waitTask(r.TaskID, 30*time.Second, TaskRunning)
	t1 := f.send(r.AgentID, "follow ONE", "f1")
	t2 := f.send(r.AgentID, "follow TWO", "f2")
	t3 := f.send(r.AgentID, "follow THREE", "f3")
	if _, err := f.m.Cancel(t2); err != nil {
		t.Fatal(err)
	}
	q, err := f.m.Queue(r.AgentID, 0)
	if err != nil || len(q.Pending) != 2 || q.Pending[0].TaskID != t1 || q.Pending[1].TaskID != t3 {
		t.Fatalf("queue: %+v %v", q, err)
	}
	d1 := f.waitTask(t1, 90*time.Second, TaskCompleted, TaskFailed)
	d3 := f.waitTask(t3, 60*time.Second, TaskCompleted, TaskFailed)
	if d1.State != TaskCompleted || d3.State != TaskCompleted || !d1.FinishedAt.Before(*d3.DeliveredAt) {
		t.Fatalf("order: one=%+v three=%+v", d1, d3)
	}
	if !strings.Contains(d1.Excerpt, "ECHO: follow ONE") || strings.Contains(d1.Excerpt, "THREE") {
		t.Fatalf("one: %q", d1.Excerpt)
	}
	if tv, _, _ := f.m.TaskStatus(t2); tv.State != TaskCancelled || tv.DeliveredAt != nil {
		t.Fatalf("cancelled follow-up ran: %+v", tv)
	}
	if strings.Contains(f.pane(r.AgentID), "follow TWO") {
		t.Fatal("cancelled prompt reached Pi")
	}
}

// Takeover after the cancel request but before the bridge acts: the abort
// is withheld (never executed under human control) and a draft the human
// types afterwards is left alone.
func TestE2ECancelOvertakenByTakeoverIsWithheldAndDraftPreserved(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("long SLOW", "k1")
	f.waitTask(r.TaskID, 30*time.Second, TaskRunning)
	f.waitAgent(r.AgentID, 10*time.Second, func(v *AgentView) bool {
		tv, _, _ := f.m.TaskStatus(r.TaskID)
		return tv.Phase == PhaseAccepted
	}, "accepted")
	// Request the cancel and move control to the human under the same lock,
	// before the bridge's next tick: the request was valid when made, and
	// control changed before the bridge could act.
	if err := f.m.Store.WithAgentLock(r.AgentID, func() error {
		a, err := f.m.Store.LoadAgent(r.AgentID)
		if err != nil {
			return err
		}
		tk, err := f.m.Store.LoadTask(r.AgentID, r.TaskID)
		if err != nil {
			return err
		}
		now := f.m.Store.now()
		tk.CancelRequested, tk.CancelRequestedAt, tk.CancelLeaseGeneration = true, &now, a.Lease.Generation
		if err := f.m.Store.SaveTask(tk); err != nil {
			return err
		}
		a.Lease = Lease{Holder: LeaseHuman, Generation: a.Lease.Generation + 1, Since: now, Reason: "explicit_takeover", By: "test"}
		return f.m.Store.SaveAgent(a)
	}); err != nil {
		t.Fatal(err)
	}
	f.humanType(r.AgentID, "human draft kept", false)
	deadline := time.Now().Add(10 * time.Second)
	for {
		tv, _, _ := f.m.TaskStatus(r.TaskID)
		if tv.CancelOutcome == CancelWithheld {
			if tv.AbortWithheld != "human_control" || tv.AbortSentAt != nil {
				t.Fatalf("withheld: %+v", tv)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("abort not withheld: %+v events=%s", tv, eventTypes(f.events(r.AgentID)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The turn keeps running (no abort), and the draft is intact.
	time.Sleep(500 * time.Millisecond)
	if tv, _, _ := f.m.TaskStatus(r.TaskID); tv.State != TaskRunning {
		t.Fatalf("turn should keep running under the human: %+v", tv)
	}
	v := f.waitAgent(r.AgentID, 5*time.Second, func(v *AgentView) bool { return v.Bridge != nil && v.Bridge.HumanTyping }, "draft present")
	_ = v
	if !strings.Contains(f.pane(r.AgentID), "human draft kept") {
		t.Fatalf("draft lost:\n%s", f.pane(r.AgentID))
	}
	if strings.Contains(eventTypes(f.events(r.AgentID)), "abort_sent") {
		t.Fatal("abort sent under human control")
	}
}

// A cancel that lands after the hand-off but before Pi accepted the prompt
// drops the injection: Pi never runs it.
func TestE2ECancelBeforeAcceptanceDropsInjection(t *testing.T) {
	f := newPiFixture(t, "DEVX_PI_TEST_DELIVERY_DELAY_MS=3000")
	r := f.start("warmup", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	task := f.send(r.AgentID, "never accepted prompt", "k2")
	if tv := f.waitTask(task, 15*time.Second, TaskRunning); tv.Phase != PhaseDelivered { // claimed, inside the delay
		t.Fatalf("phase=%s", tv.Phase)
	}
	if _, err := f.m.Cancel(task); err != nil {
		t.Fatal(err)
	}
	tv := f.waitTask(task, 15*time.Second, TaskCancelled, TaskCompleted, TaskFailed)
	if tv.State != TaskCancelled || tv.CancelOutcome != CancelBeforeAcceptance || tv.AbortSentAt != nil {
		t.Fatalf("%+v events=%s", tv, eventTypes(f.events(r.AgentID)))
	}
	time.Sleep(time.Second)
	if strings.Contains(f.pane(r.AgentID), "never accepted prompt") {
		t.Fatalf("cancelled prompt reached Pi:\n%s", f.pane(r.AgentID))
	}
	if v, _ := f.m.AgentStatus(r.AgentID); v.Agent.Lease.Holder != LeaseManaged {
		t.Fatalf("lease=%s", v.Agent.Lease.Holder)
	}
}

// Duplicate sends (retry after a dropped response) produce exactly one
// turn in Pi.
func TestE2EDuplicateSendRunsOnce(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("warmup", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	a := f.send(r.AgentID, "only once MARKER-7", "dup")
	b := f.send(r.AgentID, "only once MARKER-7", "dup")
	if a != b {
		t.Fatalf("retry created a second task: %s %s", a, b)
	}
	f.waitTask(a, 45*time.Second, TaskCompleted)
	time.Sleep(time.Second)
	if n := strings.Count(f.pane(r.AgentID), "ECHO: only once MARKER-7"); n != 1 {
		t.Fatalf("turn ran %d times:\n%s", n, f.pane(r.AgentID))
	}
}
