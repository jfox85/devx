package cmd

import (
	"bytes"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
)

type recoverFake struct {
	live      map[string]bool
	liveErr   error
	paths     map[string]bool
	views     []*piagent.AgentView
	status    map[string]*piagent.AgentView // re-check answers; defaults to views
	ensureErr map[string]error
	relErr    map[string]error
	ensured   []string
	relaunch  []string
	targetUp  bool
}

func (f *recoverFake) deps() recoverDeps {
	return recoverDeps{
		liveTmux:      func() (map[string]bool, error) { return f.live, f.liveErr },
		targetRunning: func(session.TargetMeta) bool { return f.targetUp },
		pathExists:    func(p string) bool { return f.paths[p] },
		ensureTmux: func(name string, _ *session.Session) error {
			f.ensured = append(f.ensured, name)
			return f.ensureErr[name]
		},
		agents: func() ([]*piagent.AgentView, error) { return f.views, nil },
		agentStatus: func(id string) (*piagent.AgentView, error) {
			if v, ok := f.status[id]; ok {
				return v, nil
			}
			for _, v := range f.views {
				if v.Agent.ID == id {
					return v, nil
				}
			}
			return nil, errors.New("not found")
		},
		relaunch: func(id string) error {
			f.relaunch = append(f.relaunch, id)
			return f.relErr[id]
		},
	}
}

var recoverNow = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// hostSess is a normal session with a worktree and a tmuxp template.
func hostSess(f *recoverFake, name string, active time.Time) *session.Session {
	p := "/wt/" + name
	f.paths[p] = true
	f.paths[filepath.Join(p, ".tmuxp.yaml")] = true
	return &session.Session{Name: name, Path: p, LastAttached: active}
}

// loSess is a local-only session owned by agentID.
func loSess(f *recoverFake, name, agentID string, active time.Time) *session.Session {
	p := "/wt/" + name
	f.paths[p] = true
	return &session.Session{Name: name, Path: p, LastAttached: active,
		LocalOnly: &session.LocalOnlyMeta{AgentID: agentID, Owner: session.LocalOnlyOwnerPiMCP}}
}

func crashedAgent(id, sess string, lastSeen time.Time) *piagent.AgentView {
	return &piagent.AgentView{
		Agent:  &piagent.Agent{ID: id, DevxSession: sess},
		State:  piagent.AgentPaneExited,
		Bridge: &piagent.Bridge{Heartbeat: lastSeen, Mode: "active"},
	}
}

func newRecoverFake() *recoverFake {
	return &recoverFake{live: map[string]bool{}, paths: map[string]bool{}, ensureErr: map[string]error{}, relErr: map[string]error{}}
}

func recoverOpts() recoverOptions { return recoverOptions{Cutoff: recoverNow.Add(-24 * time.Hour)} }

func findItem(t *testing.T, p *recoverPlan, name string) recoverSessionItem {
	t.Helper()
	for _, it := range p.Items {
		if it.Session == name {
			return it
		}
	}
	t.Fatalf("session %q not in plan: %+v", name, p.Items)
	return recoverSessionItem{}
}

func TestRecoverPlanAfterCrash(t *testing.T) {
	f := newRecoverFake()
	recent := recoverNow.Add(-time.Hour)
	old := recoverNow.Add(-10 * 24 * time.Hour)
	sessions := map[string]*session.Session{
		"human":    hostSess(f, "human", recent),
		"alive":    hostSess(f, "alive", recent),
		"stale":    hostSess(f, "stale", old),
		"agent-lo": loSess(f, "agent-lo", "pa_aaaaaaaaaaaa", old), // selected via its crashed agent
	}
	f.live["alive"] = true
	f.views = []*piagent.AgentView{crashedAgent("pa_aaaaaaaaaaaa", "agent-lo", recent)}

	plan, err := planRecover(sessions, f.deps(), recoverOpts())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ignored != 1 || len(plan.Items) != 3 {
		t.Fatalf("want 3 items and 1 ignored, got %d/%d: %+v", len(plan.Items), plan.Ignored, plan.Items)
	}
	if it := findItem(t, plan, "human"); it.Tmux != recoverActRecreate {
		t.Fatalf("human: %+v", it)
	}
	if it := findItem(t, plan, "alive"); it.Tmux != recoverActRunning {
		t.Fatalf("alive: %+v", it)
	}
	lo := findItem(t, plan, "agent-lo")
	if lo.Tmux != recoverActViaAgent || len(lo.Agents) != 1 || lo.Agents[0].Action != recoverActRelaunch {
		t.Fatalf("agent-lo: %+v", lo)
	}

	executeRecover(plan, sessions, f.deps(), recoverOpts())
	if strings.Join(f.ensured, ",") != "human" {
		t.Fatalf("ensured %v; must recreate only the missing non-local-only session", f.ensured)
	}
	if strings.Join(f.relaunch, ",") != "pa_aaaaaaaaaaaa" {
		t.Fatalf("relaunched %v", f.relaunch)
	}
	if recoverFailures(plan) != 0 {
		t.Fatalf("failures: %+v", plan.Items)
	}
}

func TestRecoverDryRunChangesNothing(t *testing.T) {
	f := newRecoverFake()
	sessions := map[string]*session.Session{"human": hostSess(f, "human", recoverNow)}
	if _, err := planRecover(sessions, f.deps(), recoverOpts()); err != nil {
		t.Fatal(err)
	}
	if len(f.ensured)+len(f.relaunch) != 0 {
		t.Fatalf("planning must not act: %v %v", f.ensured, f.relaunch)
	}
}

func TestRecoverAgentDecisions(t *testing.T) {
	cutoff := recoverNow.Add(-24 * time.Hour)
	recent := recoverNow.Add(-time.Hour)
	retired := recoverNow
	cases := map[string]struct {
		v       *piagent.AgentView
		include bool
		want    bool
	}{
		"crashed recently": {v: crashedAgent("pa_1", "s", recent), want: true},
		"crashed long ago": {v: crashedAgent("pa_1", "s", recoverNow.Add(-48*time.Hour))},
		"idle":             {v: &piagent.AgentView{Agent: &piagent.Agent{ID: "pa_1"}, State: piagent.AgentIdle, Bridge: &piagent.Bridge{Heartbeat: recent}}},
		"tmux unknown":     {v: &piagent.AgentView{Agent: &piagent.Agent{ID: "pa_1"}, State: piagent.AgentUnknown, Bridge: &piagent.Bridge{Heartbeat: recent}}},
		"no bridge":        {v: &piagent.AgentView{Agent: &piagent.Agent{ID: "pa_1"}, State: piagent.AgentPaneExited}},
		"adopted":          {v: func() *piagent.AgentView { v := crashedAgent("pa_1", "s", recent); v.Agent.Adopted = true; return v }()},
		"retired": {v: func() *piagent.AgentView {
			v := crashedAgent("pa_1", "s", recent)
			v.Agent.RetiredAt = &retired
			return v
		}()},
		// tmux kill-server/reboot hangs Pi up: its shutdown hook writes
		// "stopped", but the pane is gone too. That is still a crash.
		"hung up by tmux loss": {v: func() *piagent.AgentView { v := crashedAgent("pa_1", "s", recent); v.Bridge.Mode = "stopped"; return v }(), want: true},
		"exited, pane kept":    {v: func() *piagent.AgentView { v := crashedAgent("pa_1", "s", recent); v.PaneAlive = true; return v }()},
		"exited, included": {v: func() *piagent.AgentView { v := crashedAgent("pa_1", "s", recent); v.PaneAlive = true; return v }(),
			include: true, want: true},
	}
	for name, c := range cases {
		got, reason := agentRecoverDecision(c.v, cutoff, c.include)
		if got != c.want {
			t.Errorf("%s: got %v (%s), want %v", name, got, reason, c.want)
		}
		if reason == "" {
			t.Errorf("%s: empty reason", name)
		}
	}
}

func TestRecoverSkipsUnrecoverableSessions(t *testing.T) {
	f := newRecoverFake()
	noTmuxp := hostSess(f, "no-tmuxp", recoverNow)
	delete(f.paths, filepath.Join(noTmuxp.Path, ".tmuxp.yaml"))
	gone := hostSess(f, "gone", recoverNow)
	delete(f.paths, gone.Path)
	docker := hostSess(f, "docker", recoverNow)
	docker.Target = session.TargetMeta{Type: "docker", ContainerName: "c1"}
	// Crashed agent whose session lost its local-only marker: relaunch
	// would be denied, so it is not planned.
	unmarked := hostSess(f, "unmarked", recoverNow)
	f.views = []*piagent.AgentView{crashedAgent("pa_bbbbbbbbbbbb", "unmarked", recoverNow)}
	sessions := map[string]*session.Session{"no-tmuxp": noTmuxp, "gone": gone, "docker": docker, "unmarked": unmarked}

	plan, err := planRecover(sessions, f.deps(), recoverOpts())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"no-tmuxp", "gone", "docker"} {
		if it := findItem(t, plan, n); it.Tmux != recoverActSkip || it.Reason == "" {
			t.Errorf("%s: %+v", n, it)
		}
	}
	if it := findItem(t, plan, "unmarked"); it.Agents[0].Action != recoverActSkip || !strings.Contains(it.Agents[0].Reason, "local-only marker") {
		t.Errorf("unmarked: %+v", it)
	}
	executeRecover(plan, sessions, f.deps(), recoverOpts())
	if len(f.relaunch) != 0 {
		t.Fatalf("relaunched %v", f.relaunch)
	}
	if strings.Join(f.ensured, ",") != "unmarked" {
		t.Fatalf("ensured %v", f.ensured)
	}
}

func TestRecoverFailsClosedWhenTmuxUnreachable(t *testing.T) {
	f := newRecoverFake()
	f.liveErr = errors.New("permission denied")
	sessions := map[string]*session.Session{"human": hostSess(f, "human", recoverNow)}
	if _, err := planRecover(sessions, f.deps(), recoverOpts()); err == nil {
		t.Fatal("want error when tmux cannot be queried")
	}
}

func TestRecoverNamedSessionsAndUnknownName(t *testing.T) {
	f := newRecoverFake()
	old := recoverNow.Add(-30 * 24 * time.Hour)
	sessions := map[string]*session.Session{"a": hostSess(f, "a", old), "b": hostSess(f, "b", recoverNow)}
	opts := recoverOpts()
	opts.Names = []string{"a"}
	plan, err := planRecover(sessions, f.deps(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Session != "a" || plan.Items[0].Tmux != recoverActRecreate {
		t.Fatalf("named old session must be selected alone: %+v", plan.Items)
	}
	opts.Names = []string{"nope"}
	if _, err := planRecover(sessions, f.deps(), opts); err == nil {
		t.Fatal("unknown session name must error")
	}
	opts.Names, opts.All = nil, true
	if plan, _ := planRecover(sessions, f.deps(), opts); len(plan.Items) != 2 {
		t.Fatalf("--all: %+v", plan.Items)
	}
}

func TestRecoverExecuteRechecksAndReportsFailures(t *testing.T) {
	f := newRecoverFake()
	sessions := map[string]*session.Session{
		"broken": hostSess(f, "broken", recoverNow),
		"lo1":    loSess(f, "lo1", "pa_111111111111", recoverNow),
		"lo2":    loSess(f, "lo2", "pa_222222222222", recoverNow),
	}
	f.views = []*piagent.AgentView{
		crashedAgent("pa_111111111111", "lo1", recoverNow),
		crashedAgent("pa_222222222222", "lo2", recoverNow),
	}
	f.ensureErr["broken"] = errors.New("tmuxp failed")
	f.relErr["pa_222222222222"] = errors.New("denied")
	plan, err := planRecover(sessions, f.deps(), recoverOpts())
	if err != nil {
		t.Fatal(err)
	}
	// Between plan and execute someone else relaunched pa_1.
	f.status = map[string]*piagent.AgentView{"pa_111111111111": {Agent: &piagent.Agent{ID: "pa_111111111111"}, State: piagent.AgentIdle}}
	executeRecover(plan, sessions, f.deps(), recoverOpts())
	if strings.Join(f.relaunch, ",") != "pa_222222222222" {
		t.Fatalf("relaunched %v; a recovered agent must not be relaunched twice", f.relaunch)
	}
	if got := recoverFailures(plan); got != 2 {
		t.Fatalf("want 2 failures (broken tmux, denied relaunch), got %d: %+v", got, plan.Items)
	}
	var buf bytes.Buffer
	printRecoverPlan(&buf, plan)
	out := buf.String()
	for _, want := range []string{"tmuxp failed", "denied", "no longer needs recovery"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// Real tmux, through the test wrapper's owned socket: a live session is
// listed and, once it is killed, absent again.
func TestLiveTmuxSessionsReal(t *testing.T) {
	requireIsolatedTestEnv(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", "recover-live", "sleep", "600").CombinedOutput(); err != nil {
		t.Fatalf("new-session: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "=recover-live").Run() })
	live, err := liveTmuxSessions()
	if err != nil || !live["recover-live"] {
		t.Fatalf("live=%v err=%v", live, err)
	}
	if err := exec.Command("tmux", "kill-session", "-t", "=recover-live").Run(); err != nil {
		t.Fatal(err)
	}
	live, err = liveTmuxSessions()
	if err != nil || live["recover-live"] {
		t.Fatalf("after kill: live=%v err=%v", live, err)
	}
}

func TestTmuxNoServer(t *testing.T) {
	for msg, want := range map[string]bool{
		"no server running on /tmp/tmux-501/default":                            true,
		"error connecting to /tmp/tmux-501/default (No such file or directory)": true,
		"error connecting to /tmp/tmux-501/default (Permission denied)":         false,
		"server exited unexpectedly":                                            false,
	} {
		if got := tmuxNoServer(msg); got != want {
			t.Errorf("%q: got %v want %v", msg, got, want)
		}
	}
}
