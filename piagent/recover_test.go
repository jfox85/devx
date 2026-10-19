package piagent

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// paneTmux answers display-message for one pane id with the given fields and
// records every other command. Never reaches a tmux server.
type paneTmux struct {
	mu     sync.Mutex
	paneID string
	fields string // tab-joined fields after the id; "" = pane absent
	calls  []string
}

func (p *paneTmux) exec(args ...string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, strings.Join(args, " "))
	switch args[0] {
	case "display-message":
		if args[3] == p.paneID && p.fields != "" {
			return p.paneID + "\t" + p.fields, nil
		}
		return "", fmt.Errorf("can't find pane: %s", args[3])
	case "new-window":
		return "@9 %9", nil
	}
	return "", nil
}

func (p *paneTmux) ran(prefix string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// crashedSeed is an agent bound to %3 in session s1 whose Pi is confirmed
// dead (bridge stopped), with one task left running.
func crashedSeed(t *testing.T, m *Manager) (*Agent, string) {
	t.Helper()
	a := seedAgent(t, m)
	r, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	err = m.Store.WithAgentLock(a.ID, func() error {
		a, _ := m.Store.LoadAgent(a.ID)
		a.Binding = Binding{TmuxSession: "s1", WindowID: "@1", PaneID: "%3"}
		a.LaunchCount, a.LaunchNonce = 1, "n1"
		if err := m.Store.SaveAgent(a); err != nil {
			return err
		}
		tk, _ := m.Store.LoadTask(a.ID, r.TaskID)
		tk.State, tk.BridgeInstance = TaskRunning, "inst-1"
		if err := m.Store.SaveTask(tk); err != nil {
			return err
		}
		return writeJSONAtomic(m.Store.bridgePath(a.ID), &Bridge{Instance: "inst-1", Nonce: "n1", Pane: "%3", PID: 1 << 30,
			Heartbeat: time.Now().Add(-time.Hour), Mode: "stopped"})
	})
	if err != nil {
		t.Fatal(err)
	}
	a, _ = m.Store.LoadAgent(a.ID)
	return a, r.TaskID
}

func launchCmd(m *Manager, a *Agent) string {
	return filepath.Join(m.Store.AgentDir(a.ID), "launch.sh")
}

// After a tmux server restart the bound id %3 names an unrelated pane in the
// agent's own (recreated) session. It must be reported as foreign and never
// respawned: relaunch opens a new pi-agent window instead.
func TestRestartedServerReusedPaneIsForeignAndNotRespawned(t *testing.T) {
	m, _ := newTestManager(t)
	a, _ := crashedSeed(t, m)
	m.Creator = okTmuxCreator{m.Creator}
	pt := &paneTmux{paneID: "%3", fields: "s1\t@1\t0\t100\ts1\ttail\ttail -f /dev/null"}
	m.Tmux = Tmux{Exec: pt.exec}

	v, err := m.PeekStatus(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !v.PaneForeign || !v.BridgeDead {
		t.Fatalf("foreign=%v dead=%v state=%s", v.PaneForeign, v.BridgeDead, v.State)
	}
	if err := m.RelaunchIfUnchanged(a.ID, 1); err != nil {
		t.Fatal(err)
	}
	if pt.ran("respawn-pane") || !pt.ran("new-window") {
		t.Fatalf("must open a new window, never respawn a foreign pane: %v", pt.calls)
	}
	got, _ := m.Store.LoadAgent(a.ID)
	if got.Binding.PaneID != "%9" || got.LaunchCount != 2 {
		t.Fatalf("binding %+v launch %d", got.Binding, got.LaunchCount)
	}
}

// The agent's own pane (started with its launch.sh) is still respawned in
// place, as before.
func TestOwnPaneIsRespawned(t *testing.T) {
	m, _ := newTestManager(t)
	a, _ := crashedSeed(t, m)
	m.Creator = okTmuxCreator{m.Creator}
	pt := &paneTmux{paneID: "%3", fields: "s1\t@1\t0\t100\ts1\tsh\t" + launchCmd(m, a)}
	m.Tmux = Tmux{Exec: pt.exec}
	if v, _ := m.PeekStatus(a.ID); v.PaneForeign {
		t.Fatal("own pane reported foreign")
	}
	if err := m.Relaunch(a.ID, false); err != nil {
		t.Fatal(err)
	}
	if !pt.ran("respawn-pane") || pt.ran("new-window") {
		t.Fatalf("own pane must be respawned in place: %v", pt.calls)
	}
}

// tmux builds that do not report pane_start_command keep the old behavior.
func TestPaneWithoutStartCommandIsNotForeign(t *testing.T) {
	a := &Agent{ID: "pa_000000000001", LaunchCount: 1}
	if !paneIsAgents(PaneInfo{Exists: true}, a) {
		t.Fatal("unknown start command must not be treated as foreign")
	}
	if paneIsAgents(PaneInfo{Exists: true, HasStartCommand: true, StartCommand: "bash"}, a) {
		t.Fatal("bash pane must be foreign")
	}
	adopted := &Agent{ID: "pa_000000000001", Adopted: true}
	if !paneIsAgents(PaneInfo{Exists: true, HasStartCommand: true, StartCommand: "pi"}, adopted) {
		t.Fatal("adopted, never-relaunched pane runs the human's command and must not be foreign")
	}
}

// PeekStatus never writes: orphaned running tasks stay running and no
// events are appended. AgentStatus still reconciles.
func TestPeekStatusDoesNotReconcile(t *testing.T) {
	m, _ := newTestManager(t)
	a, taskID := crashedSeed(t, m)
	pt := &paneTmux{paneID: "%3"} // pane gone
	m.Tmux = Tmux{Exec: pt.exec}
	_, seqBefore, _ := m.Store.ReadEvents(a.ID, 0, 1)

	v, err := m.PeekStatus(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != AgentPaneExited {
		t.Fatalf("state %s", v.State)
	}
	if tk, _ := m.Store.LoadTask(a.ID, taskID); tk.State != TaskRunning {
		t.Fatalf("peek reconciled the task: %s", tk.State)
	}
	if _, seq, _ := m.Store.ReadEvents(a.ID, 0, 1); seq != seqBefore {
		t.Fatalf("peek appended events: %d -> %d", seqBefore, seq)
	}
	if _, err := m.AgentStatus(a.ID); err != nil {
		t.Fatal(err)
	}
	if tk, _ := m.Store.LoadTask(a.ID, taskID); tk.State != TaskUnknown {
		t.Fatalf("AgentStatus must still reconcile: %s", tk.State)
	}
}

// Two recoveries that both saw the agent at launch 1 start exactly one Pi.
func TestConcurrentRelaunchIfUnchangedStartsOnePi(t *testing.T) {
	m, _ := newTestManager(t)
	a, _ := crashedSeed(t, m)
	m.Creator = okTmuxCreator{m.Creator}
	pt := &paneTmux{paneID: "%3"}
	m.Tmux = Tmux{Exec: pt.exec}

	const n = 4
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = m.RelaunchIfUnchanged(a.ID, 1)
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrLaunchedSinceCheck):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	got, _ := m.Store.LoadAgent(a.ID)
	if ok != 1 || got.LaunchCount != 2 {
		t.Fatalf("ok=%d launchCount=%d; want exactly one launch", ok, got.LaunchCount)
	}
	windows := 0
	for _, c := range pt.calls {
		if strings.HasPrefix(c, "new-window") {
			windows++
		}
	}
	if windows != 1 {
		t.Fatalf("%d windows opened", windows)
	}
}
