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
	types := eventTypes(f.events(r.AgentID))
	if !strings.Contains(types, "cancel_requested,abort_sent") && !strings.Contains(types, "abort_sent") {
		t.Fatalf("no abort_sent event: %s", types)
	}
	if strings.Count(types, "abort_sent") != 1 {
		t.Fatalf("abort sent more than once: %s", types)
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

// Takeover after the cancel request but before the bridge acts: the abort
// is withheld (never executed under human control) and the human's draft
// in the editor is preserved byte for byte.
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
	f := newPiFixture(t, "DEVX_PI_TEST_DELIVERY_DELAY_MS=1500")
	r := f.start("warmup", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	task := f.send(r.AgentID, "never accepted prompt", "k2")
	f.waitTask(task, 15*time.Second, TaskRunning) // claimed, inside the delay
	if tv, _, _ := f.m.TaskStatus(task); tv.Phase != PhaseDelivered {
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
