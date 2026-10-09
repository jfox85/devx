package piagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// adoptCreator is a dirCreator that can also adopt "human" sessions.
type adoptCreator struct {
	*dirCreator
	human   map[string]string // existing human sessions -> worktree path
	adopted map[string]string // session -> agent id
	refuse  map[string]bool   // sessions the store refuses (local-only/container)
}

func (c *adoptCreator) Adopt(name, agentID string) (AdoptedSession, error) {
	path, ok := c.human[name]
	if !ok || c.refuse[name] {
		return AdoptedSession{}, Denied("session %q cannot be adopted", name)
	}
	if cur := c.adopted[name]; cur != "" && cur != agentID {
		return AdoptedSession{}, Denied("session %q is already managed by agent %s", name, cur)
	}
	c.adopted[name] = agentID
	return AdoptedSession{Name: name, Path: path, Project: "proj", TmuxName: name}, nil
}

func (c *adoptCreator) ReleaseAdoption(name, agentID string) error {
	if c.adopted[name] == agentID {
		delete(c.adopted, name)
	}
	return nil
}

// fakeTmux answers display-message for known panes and records every other
// command, so tests can prove adoption runs no mutating tmux command.
type fakeTmux struct {
	panes map[string]string // %id -> tab-joined display-message fields after the id
	calls []string
}

func (f *fakeTmux) exec(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if len(args) >= 4 && args[0] == "display-message" {
		if rest, ok := f.panes[args[3]]; ok {
			return args[3] + "\t" + rest, nil
		}
		return "", fmt.Errorf("can't find pane")
	}
	return "", fmt.Errorf("tmux %s not allowed in this test", args[0])
}

func newAdoptManager(t *testing.T) (*Manager, *adoptCreator, *fakeTmux) {
	t.Helper()
	m, dc := newTestManager(t)
	ft := &fakeTmux{panes: map[string]string{}}
	m.Tmux = Tmux{Exec: ft.exec}
	wt := filepath.Join(t.TempDir(), "human-wt")
	_ = os.MkdirAll(wt, 0o700)
	ac := &adoptCreator{dirCreator: dc, human: map[string]string{"human-sess": wt}, adopted: map[string]string{}, refuse: map[string]bool{}}
	m.Creator = ac
	// %7: Pi in human-sess window @3, which is also linked into the grouped
	// viewer "human-sess-web" (tmux reports the viewer as session_name).
	ft.panes["%7"] = "human-sess-web\t@3\t0\t4242\thuman-sess,human-sess-web\tpi"
	ft.panes["%8"] = "human-sess\t@4\t0\t4243\thuman-sess\tbash"
	ft.panes["%9"] = "other-sess\t@5\t0\t4244\tother-sess\tpi"
	return m, ac, ft
}

func TestAdoptRegistersWithoutTouchingPiAndIsIdempotent(t *testing.T) {
	m, ac, ft := newAdoptManager(t)
	req := AdoptRequest{Session: "human-sess", PaneID: "%7", PiSessionID: "01a1229c-a4f8-7005-9d41-c0bf435b403a", By: "jon"}
	r, err := m.Adopt(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.AgentID, "pa_") || r.Replayed {
		t.Fatalf("result %+v", r)
	}
	for _, c := range ft.calls {
		if !strings.HasPrefix(c, "display-message") {
			t.Fatalf("adopt ran a non-read tmux command: %q", c)
		}
	}
	a, _ := m.Store.LoadAgent(r.AgentID)
	if !a.Adopted || a.Lease.Holder != LeaseHuman || a.PiSessionID != req.PiSessionID || a.Binding.PaneID != "%7" || a.Binding.WindowID != "@3" || a.Binding.TmuxSession != "human-sess" {
		t.Fatalf("agent %+v", a)
	}
	if ac.adopted["human-sess"] != r.AgentID {
		t.Fatalf("session marker %v", ac.adopted)
	}
	// Grouped viewer session does not make the binding stale; pending relaunch.
	v, err := m.AgentStatus(r.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	if !v.BindingOK || v.State != AgentAdoptedPending {
		t.Fatalf("state=%s bindingOK=%v detail=%s", v.State, v.BindingOK, v.Detail)
	}
	// Same request again: same agent, no second record.
	r2, err := m.Adopt(req)
	if err != nil || r2.AgentID != r.AgentID || !r2.Replayed {
		t.Fatalf("replay %+v %v", r2, err)
	}
	agents, _ := m.Store.ListAgents()
	if len(agents) != 1 {
		t.Fatalf("agents=%d", len(agents))
	}
	// A different pane/Pi session for an already managed session is refused.
	if _, err := m.Adopt(AdoptRequest{Session: "human-sess", PaneID: "%7", PiSessionID: "other-id"}); !IsPermissionDenied(err) {
		t.Fatalf("want denied, got %v", err)
	}
}

func TestAdoptRefusesWrongPaneCommandSessionAndBadInput(t *testing.T) {
	m, ac, _ := newAdoptManager(t)
	cases := []struct {
		name string
		req  AdoptRequest
		deny bool
	}{
		{"not pi", AdoptRequest{Session: "human-sess", PaneID: "%8", PiSessionID: "s1"}, true},
		{"pane in other session", AdoptRequest{Session: "human-sess", PaneID: "%9", PiSessionID: "s1"}, true},
		{"unknown session", AdoptRequest{Session: "nope", PaneID: "%7", PiSessionID: "s1"}, true},
		{"missing pane", AdoptRequest{Session: "human-sess", PaneID: "%99", PiSessionID: "s1"}, false},
		{"bad pane id", AdoptRequest{Session: "human-sess", PaneID: "7", PiSessionID: "s1"}, false},
		{"bad pi session", AdoptRequest{Session: "human-sess", PaneID: "%7", PiSessionID: "../x"}, false},
	}
	for _, tc := range cases {
		_, err := m.Adopt(tc.req)
		if err == nil {
			t.Fatalf("%s: adopted", tc.name)
		}
		if tc.deny && !IsPermissionDenied(err) {
			t.Fatalf("%s: want permission denied, got %v", tc.name, err)
		}
		if ac.adopted["human-sess"] != "" {
			t.Fatalf("%s: session marker left behind: %v", tc.name, ac.adopted)
		}
	}
	ac.refuse["human-sess"] = true // e.g. local-only or container target
	if _, err := m.Adopt(AdoptRequest{Session: "human-sess", PaneID: "%7", PiSessionID: "s1"}); !IsPermissionDenied(err) {
		t.Fatalf("want denied for refused session, got %v", err)
	}
	if agents, _ := m.Store.ListAgents(); len(agents) != 0 {
		t.Fatalf("agents created on failure: %d", len(agents))
	}
}

func TestAdoptedRelaunchNeedsForceAndNeverCreatesWindows(t *testing.T) {
	m, _, ft := newAdoptManager(t)
	r, err := m.Adopt(AdoptRequest{Session: "human-sess", PaneID: "%7", PiSessionID: "01a1-test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Relaunch(r.AgentID, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("relaunch without --force must refuse, got %v", err)
	}
	// Pane gone: an adopted agent must never fall back to new-window.
	delete(ft.panes, "%7")
	ft.calls = nil
	err = m.Relaunch(r.AgentID, true)
	if !IsPermissionDenied(err) {
		t.Fatalf("want denied when the adopted pane is gone, got %v", err)
	}
	for _, c := range ft.calls {
		if strings.HasPrefix(c, "new-window") || strings.HasPrefix(c, "respawn-pane") {
			t.Fatalf("ran %q", c)
		}
	}
	// Launch script resumes the same Pi session, loads the bridge, keeps the
	// owner's session name.
	a, _ := m.Store.LoadAgent(r.AgentID)
	s := m.launchScript(a, "nonce", "/b/devx-bridge.ts")
	if !strings.Contains(s, "'--session-id' '01a1-test'") || !strings.Contains(s, "'-e' '/b/devx-bridge.ts'") || strings.Contains(s, "'--name'") {
		t.Fatalf("launch script:\n%s", s)
	}
}

func TestGroupedViewerSessionIsNotStale(t *testing.T) {
	m, _ := newTestManager(t)
	ft := &fakeTmux{panes: map[string]string{
		"%1": "sess-web\t@2\t0\t1\tsess,sess-web\tpi",
		"%3": "sess\t@9\t0\t1\tsess\tbash",
	}}
	m.Tmux = Tmux{Exec: ft.exec}
	now := time.Now()
	seed := func(id, pane, win string) {
		_ = m.Store.ensureAgentDirs(id)
		_ = m.Store.WithAgentLock(id, func() error {
			return m.Store.SaveAgent(&Agent{ID: id, DevxSession: "sess", Worktree: t.TempDir(), PiSessionID: "x",
				Binding: Binding{TmuxSession: "sess", WindowID: win, PaneID: pane}, Lease: Lease{Holder: LeaseManaged, Generation: 1, Since: now}, CreatedAt: now})
		})
	}
	seed("pa_000000000001", "%1", "@2")
	if v, _ := m.AgentStatus("pa_000000000001"); !v.BindingOK || v.State == AgentStaleBind {
		t.Fatalf("grouped viewer reported stale: %s %s", v.State, v.Detail)
	}
	// A pane really in a different window is still stale.
	seed("pa_000000000002", "%3", "@2")
	if v, _ := m.AgentStatus("pa_000000000002"); v.BindingOK || v.State != AgentStaleBind {
		t.Fatalf("moved pane not stale: %s", v.State)
	}
}
