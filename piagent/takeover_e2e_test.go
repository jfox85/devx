package piagent

import (
	"strings"
	"testing"
	"time"
)

// These tests run a REAL interactive Pi TUI (faux model) in an isolated tmux
// server, with the bridge loaded, and act on it the way a human would.

func TestE2EStartCompletesInVisibleTUI(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("hello from mcp", "k-start")
	tv := f.waitTask(r.TaskID, 45*time.Second, TaskCompleted, TaskFailed, TaskUnknown)
	if tv.State != TaskCompleted {
		t.Fatalf("state=%s err=%s note=%s\n%s", tv.State, tv.Error, tv.Note, f.pane(r.AgentID))
	}
	if !strings.HasPrefix(tv.Excerpt, "ECHO: hello from mcp") {
		t.Fatalf("excerpt=%q", tv.Excerpt)
	}
	// The prompt and reply are visible in the human-facing pane.
	pane := f.pane(r.AgentID)
	if !strings.Contains(pane, "hello from mcp") || !strings.Contains(pane, "ECHO: hello from mcp") {
		t.Fatalf("prompt/reply not visible in the TUI pane:\n%s", pane)
	}
	v := f.online(r.AgentID)
	if v.Bridge.PiSessionID != r.PiSessionID {
		t.Fatalf("pi session id %s, want %s", v.Bridge.PiSessionID, r.PiSessionID)
	}
}

// Human types into the live TUI while a remote task is running: the human's
// input lands in the same Pi conversation, control moves to the human, the
// remote task is marked human_intervened, and queued remote work is held.
func TestE2EHumanTypesDuringRemoteTask(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("first SLOW", "k1")
	f.waitTask(r.TaskID, 30*time.Second, TaskRunning)
	queued := f.send(r.AgentID, "queued remote follow-up", "k2")

	f.humanType(r.AgentID, "human steering text", true)
	f.waitAgent(r.AgentID, 15*time.Second, func(v *AgentView) bool { return v.Agent.Lease.Holder == LeaseHuman }, "human lease")

	first := f.waitTask(r.TaskID, 60*time.Second, TaskCompleted, TaskCancelled, TaskFailed)
	if !first.HumanIntervened {
		t.Fatalf("first task should record human intervention: %+v", first)
	}
	// Give the bridge many ticks of opportunity; queued work must not move.
	time.Sleep(2 * time.Second)
	tv, av, err := f.m.TaskStatus(queued)
	if err != nil {
		t.Fatal(err)
	}
	if tv.State != TaskWaiting || tv.WaitingReason != WaitHumanControl {
		t.Fatalf("queued task should wait for human: state=%s reason=%s", tv.State, tv.WaitingReason)
	}
	if av.State != AgentHuman {
		t.Fatalf("agent state=%s", av.State)
	}
	if strings.Contains(f.pane(r.AgentID), "queued remote follow-up") {
		t.Fatalf("queued prompt was injected while human had control:\n%s", f.pane(r.AgentID))
	}
	// The human's message went to the same Pi conversation.
	if !strings.Contains(f.pane(r.AgentID), "human steering text") {
		t.Fatalf("human input not visible:\n%s", f.pane(r.AgentID))
	}

	// Explicit release resumes managed dispatch.
	if _, err := f.m.Release(r.AgentID, "test", false); err != nil {
		t.Fatal(err)
	}
	done := f.waitTask(queued, 45*time.Second, TaskCompleted, TaskFailed)
	if done.State != TaskCompleted || !strings.Contains(done.Excerpt, "queued remote follow-up") {
		t.Fatalf("after release: %+v", done)
	}
	// Same Pi session throughout.
	v := f.online(r.AgentID)
	if v.Bridge.PiSessionID != r.PiSessionID {
		t.Fatalf("pi session changed: %s != %s", v.Bridge.PiSessionID, r.PiSessionID)
	}
}

// Explicit takeover is a fence: a takeover that lands after the bridge
// claimed a task but before Pi accepted it must stop the injection, and the
// task must go back to waiting rather than being lost.
func TestE2ETakeoverDuringRemoteSendFencesDelivery(t *testing.T) {
	f := newPiFixture(t, "DEVX_PI_TEST_DELIVERY_DELAY_MS=1500")
	r := f.start("warmup", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)

	task := f.send(r.AgentID, "fenced prompt", "k2")
	// Wait until the bridge has claimed it (running, not yet confirmed).
	f.waitTask(task, 15*time.Second, TaskRunning)
	if _, err := f.m.Takeover(r.AgentID, "test-human"); err != nil {
		t.Fatal(err)
	}
	// Past the injected delay: the bridge tried to deliver and was fenced.
	deadline := time.Now().Add(15 * time.Second)
	for {
		tv, _, err := f.m.TaskStatus(task)
		if err != nil {
			t.Fatal(err)
		}
		if tv.State == TaskWaiting {
			if tv.WaitingReason != WaitHumanControl {
				t.Fatalf("waiting reason %s", tv.WaitingReason)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task not returned to waiting: %+v\nevents=%s", tv, eventTypes(f.events(r.AgentID)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !strings.Contains(eventTypes(f.events(r.AgentID)), "delivery_fenced") {
		t.Fatalf("expected delivery_fenced event: %s", eventTypes(f.events(r.AgentID)))
	}
	time.Sleep(1 * time.Second)
	if strings.Contains(f.pane(r.AgentID), "fenced prompt") {
		t.Fatalf("fenced prompt reached Pi:\n%s", f.pane(r.AgentID))
	}
	if _, err := f.m.Release(r.AgentID, "test-human", false); err != nil {
		t.Fatal(err)
	}
	if tv := f.waitTask(task, 45*time.Second, TaskCompleted, TaskFailed); tv.State != TaskCompleted {
		t.Fatalf("after release: %+v", tv)
	}
}

// A human who has typed (but not submitted) into the editor makes the agent
// busy: remote prompts are not delivered over a half-written human message.
func TestE2EBusyWhileHumanDraftIsInEditor(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("warmup", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	f.humanType(r.AgentID, "draft not submitted", false)
	f.waitAgent(r.AgentID, 10*time.Second, func(v *AgentView) bool { return v.Bridge != nil && v.Bridge.HumanTyping }, "human typing")
	task := f.send(r.AgentID, "must wait", "k2")
	time.Sleep(1500 * time.Millisecond)
	tv, _, _ := f.m.TaskStatus(task)
	if tv.State != TaskWaiting || tv.WaitingReason != WaitBusy {
		t.Fatalf("state=%s reason=%s", tv.State, tv.WaitingReason)
	}
	// Clear the draft (Ctrl+C clears the editor in Pi); delivery resumes.
	a, _ := f.m.Store.LoadAgent(r.AgentID)
	_, _ = f.tmux.Run("send-keys", "-t", a.Binding.PaneID, "C-c")
	if tv := f.waitTask(task, 45*time.Second, TaskCompleted, TaskFailed); tv.State != TaskCompleted {
		t.Fatalf("%+v", tv)
	}
}

func TestE2ECancelRunningAndWaiting(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("long SLOW", "k1")
	f.waitTask(r.TaskID, 30*time.Second, TaskRunning)
	waiting := f.send(r.AgentID, "never runs", "k2")
	if _, err := f.m.Cancel(waiting); err != nil {
		t.Fatal(err)
	}
	if tv, _, _ := f.m.TaskStatus(waiting); tv.State != TaskCancelled {
		t.Fatalf("waiting cancel: %s", tv.State)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := f.m.Cancel(r.TaskID); err != nil {
		t.Fatal(err)
	}
	tv := f.waitTask(r.TaskID, 30*time.Second, TaskCancelled, TaskCompleted, TaskFailed)
	if tv.State != TaskCancelled || tv.StopReason != "aborted" {
		t.Fatalf("running cancel: %+v", tv)
	}
	// Cancelling an abort must not be read as a human interruption.
	if v, _ := f.m.AgentStatus(r.AgentID); v.Agent.Lease.Holder != LeaseManaged {
		t.Fatalf("lease moved to %s on remote cancel", v.Agent.Lease.Holder)
	}
}

// A human pressing Escape in the TUI interrupts the remote turn; that is a
// takeover, not a remote cancellation.
func TestE2EHumanInterruptTakesControl(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("long SLOW", "k1")
	f.waitTask(r.TaskID, 30*time.Second, TaskRunning)
	time.Sleep(300 * time.Millisecond)
	a, _ := f.m.Store.LoadAgent(r.AgentID)
	_, _ = f.tmux.Run("send-keys", "-t", a.Binding.PaneID, "Escape")
	tv := f.waitTask(r.TaskID, 30*time.Second, TaskCancelled, TaskCompleted)
	if tv.State != TaskCancelled || !tv.HumanIntervened {
		t.Fatalf("%+v", tv)
	}
	v, _ := f.m.AgentStatus(r.AgentID)
	if v.Agent.Lease.Holder != LeaseHuman {
		t.Fatalf("lease=%s", v.Agent.Lease.Holder)
	}
	// While human holds control, remote cancel of running work is denied.
	f.humanType(r.AgentID, "human runs SLOW", true)
	time.Sleep(500 * time.Millisecond)
	next := f.send(r.AgentID, "x", "k3")
	if _, err := f.m.Cancel(next); err != nil {
		t.Fatalf("cancel of waiting task should still be allowed: %v", err)
	}
}

// Pi crashes mid-task: the task becomes unknown (not silently completed or
// retried), and relaunch resumes the SAME Pi session with its history.
func TestE2ECrashThenRelaunchResumesSameSession(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("remember the word PELICAN", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	running := f.send(r.AgentID, "crash during SLOW", "k2")
	f.waitTask(running, 30*time.Second, TaskRunning)
	v := f.online(r.AgentID)
	// SIGKILL: no shutdown hooks run.
	if err := killPID(v.Bridge.PID); err != nil {
		t.Fatal(err)
	}
	tv := f.waitTask(running, 20*time.Second, TaskUnknown)
	if tv.Note == "" {
		t.Fatalf("unknown task needs an explanation")
	}
	after := f.send(r.AgentID, "after crash", "k3")
	if tv, _, _ := f.m.TaskStatus(after); tv.State != TaskWaiting {
		t.Fatalf("after-crash task: %s", tv.State)
	}
	if err := f.m.Relaunch(r.AgentID, false); err != nil {
		t.Fatal(err)
	}
	if tv := f.waitTask(after, 45*time.Second, TaskCompleted, TaskFailed); tv.State != TaskCompleted {
		t.Fatalf("%+v", tv)
	}
	v = f.online(r.AgentID)
	if v.Bridge.PiSessionID != r.PiSessionID {
		t.Fatalf("relaunch changed session id: %s", v.Bridge.PiSessionID)
	}
	// History from before the crash is restored in the resumed TUI.
	if p := f.pane(r.AgentID); !strings.Contains(p, "PELICAN") {
		t.Fatalf("history not restored after relaunch:\n%s", p)
	}
	// Relaunching a live agent is refused without force.
	if err := f.m.Relaunch(r.AgentID, false); err == nil {
		t.Fatal("expected relaunch of live agent to be refused")
	}
}

// If the bound pane is gone or now belongs to something else, the agent is
// reported as such and nothing is delivered anywhere.
func TestE2EStalePaneBinding(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("warmup", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	a, _ := f.m.Store.LoadAgent(r.AgentID)
	// Simulate a stale record: point the binding at a pane in another
	// window (a shell, as a human pane would be).
	_, other, err := f.tmux.NewWindow(a.Binding.TmuxSession, "human-shell", a.Worktree, "sleep 600")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.m.Store.WithAgentLock(a.ID, func() error {
		a, _ := f.m.Store.LoadAgent(r.AgentID)
		a.Binding.PaneID = other
		return f.m.Store.SaveAgent(a)
	})
	v, _ := f.m.AgentStatus(r.AgentID)
	if v.State != AgentStaleBind {
		t.Fatalf("state=%s detail=%s", v.State, v.Detail)
	}
	task := f.send(r.AgentID, "must not go anywhere", "k2")
	time.Sleep(1500 * time.Millisecond)
	tv, _, _ := f.m.TaskStatus(task)
	if tv.State != TaskWaiting || tv.WaitingReason != WaitBindingStale {
		t.Fatalf("state=%s reason=%s", tv.State, tv.WaitingReason)
	}
	if out, _ := f.tmux.Capture(other, 50); strings.Contains(out, "must not go anywhere") {
		t.Fatal("prompt leaked into the stale pane")
	}
	// Kill the window: pane_exited.
	_, _ = f.tmux.Run("kill-pane", "-t", other)
	if v, _ := f.m.AgentStatus(r.AgentID); v.State != AgentPaneExited {
		t.Fatalf("state=%s", v.State)
	}
}

// Large output is returned in bounded chunks that reassemble exactly.
func TestE2EBoundedResultsAndRedaction(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("BIG:40000", "k1")
	tv := f.waitTask(r.TaskID, 60*time.Second, TaskCompleted)
	if len(tv.Excerpt) > MaxExcerptBytes || !tv.ExcerptTrunc || tv.ResultBytes < 40000 {
		t.Fatalf("excerpt %d trunc=%v total=%d", len(tv.Excerpt), tv.ExcerptTrunc, tv.ResultBytes)
	}
	var all strings.Builder
	off := 0
	for i := 0; ; i++ {
		c, err := f.m.Result(r.TaskID, off, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Text) > MaxResultChunk {
			t.Fatalf("chunk %d bytes", len(c.Text))
		}
		all.WriteString(c.Text)
		off = c.NextOffset
		if c.EOF {
			break
		}
		if i > 20 {
			t.Fatal("too many chunks")
		}
	}
	if !strings.HasSuffix(all.String(), "END-OF-BIG") || all.Len() != tv.ResultBytes {
		t.Fatalf("reassembled %d bytes", all.Len())
	}
	s := f.send(r.AgentID, "SECRET please", "k2")
	tv = f.waitTask(s, 45*time.Second, TaskCompleted)
	if strings.Contains(tv.Excerpt, "sk-proj-abcdefghij") || !strings.Contains(tv.Excerpt, "[REDACTED]") {
		t.Fatalf("not redacted: %q", tv.Excerpt)
	}
}

func TestE2EFailedTask(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("please FAIL", "k1")
	tv := f.waitTask(r.TaskID, 45*time.Second, TaskFailed, TaskCompleted)
	if tv.State != TaskFailed || tv.Error == "" {
		t.Fatalf("%+v", tv)
	}
}
