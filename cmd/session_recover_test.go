package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
)

type recoverFake struct {
	live       map[string]bool
	liveErr    error
	liveAfter  map[string]bool // tmux sessions seen at execution time (default: live)
	policyErr  error
	peekErr    map[string]error
	unreadable map[string]error
	paths      map[string]bool
	views      []*piagent.AgentView
	status     map[string]*piagent.AgentView // re-check answers; defaults to views
	ensureErr  map[string]error
	relErr     map[string]error
	ensured    []string
	relaunch   []string
	order      []string
	calls      int
	targetUp   bool
}

func (f *recoverFake) deps() recoverDeps {
	return recoverDeps{
		liveTmux: func() (map[string]bool, error) {
			if f.calls++; f.calls > 1 && f.liveAfter != nil {
				return f.liveAfter, f.liveErr
			}
			return f.live, f.liveErr
		},
		targetRunning: func(session.TargetMeta) bool { return f.targetUp },
		pathExists:    func(p string) bool { return f.paths[p] },
		ensureTmux: func(name string, _ *session.Session) error {
			f.ensured = append(f.ensured, name)
			f.order = append(f.order, "ensure:"+name)
			return f.ensureErr[name]
		},
		agents: func() ([]*piagent.Agent, map[string]error, error) {
			var out []*piagent.Agent
			for _, v := range f.views {
				out = append(out, v.Agent)
			}
			return out, f.unreadable, nil
		},
		launchPolicyErr: f.policyErr,
		owns:            managedSessionOwnedBy,
		peek: func(id string) (*piagent.AgentView, error) {
			if err := f.peekErr[id]; err != nil {
				return nil, err
			}
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
		relaunch: func(id string, _ int) error {
			f.relaunch = append(f.relaunch, id)
			f.order = append(f.order, "relaunch:"+id)
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
	return &session.Session{Name: name, Path: p, CreatedAt: active.Add(-time.Hour), LastAttached: active}
}

// loSess is a local-only session owned by agentID.
func loSess(f *recoverFake, name, agentID string, active time.Time) *session.Session {
	p := "/wt/" + name
	f.paths[p] = true
	return &session.Session{Name: name, Path: p, CreatedAt: active.Add(-time.Hour), LastAttached: active,
		LocalOnly: &session.LocalOnlyMeta{AgentID: agentID, Owner: session.LocalOnlyOwnerPiMCP}}
}

func crashedAgent(id, sess string, lastSeen time.Time) *piagent.AgentView {
	return &piagent.AgentView{
		Agent:      &piagent.Agent{ID: id, DevxSession: sess},
		State:      piagent.AgentPaneExited,
		Bridge:     &piagent.Bridge{Heartbeat: lastSeen, Mode: "active"},
		BridgeDead: true,
	}
}

func newRecoverFake() *recoverFake {
	return &recoverFake{live: map[string]bool{}, paths: map[string]bool{}, ensureErr: map[string]error{}, relErr: map[string]error{}, peekErr: map[string]error{}}
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
	// Agents first: recreated sessions must not take pane ids first.
	if strings.Join(f.order, ",") != "relaunch:pa_aaaaaaaaaaaa,ensure:human" {
		t.Fatalf("order %v", f.order)
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
		"crashed recently":          {v: crashedAgent("pa_1", "s", recent), want: true},
		"crashed long ago":          {v: crashedAgent("pa_1", "s", recoverNow.Add(-48*time.Hour))},
		"idle":                      {v: &piagent.AgentView{Agent: &piagent.Agent{ID: "pa_1"}, State: piagent.AgentIdle, Bridge: &piagent.Bridge{Heartbeat: recent}}},
		"tmux unknown":              {v: &piagent.AgentView{Agent: &piagent.Agent{ID: "pa_1"}, State: piagent.AgentUnknown, Bridge: &piagent.Bridge{Heartbeat: recent}}},
		"no bridge":                 {v: &piagent.AgentView{Agent: &piagent.Agent{ID: "pa_1"}, State: piagent.AgentPaneExited}},
		"pane gone, Pi maybe alive": {v: func() *piagent.AgentView { v := crashedAgent("pa_1", "s", recent); v.BridgeDead = false; return v }()},
		"adopted":                   {v: func() *piagent.AgentView { v := crashedAgent("pa_1", "s", recent); v.Agent.Adopted = true; return v }()},
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
	if it := findItem(t, plan, "unmarked"); it.Agents[0].Action != recoverActSkip || !strings.Contains(it.Agents[0].Reason, "not a local-only session owned by") {
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

// After a tmux server restart the old pane id can name an unrelated pane
// (PaneForeign). With the Pi confirmed dead that is still a lost pane.
func TestRecoverForeignPaneAfterServerRestart(t *testing.T) {
	recent := recoverNow.Add(-time.Hour)
	cutoff := recoverNow.Add(-24 * time.Hour)
	for _, state := range []string{piagent.AgentStaleBind} {
		v := crashedAgent("pa_1", "s", recent)
		v.State, v.PaneAlive, v.PaneForeign = state, true, true
		if ok, reason := agentRecoverDecision(v, cutoff, false); !ok {
			t.Errorf("%s with foreign pane: %s", state, reason)
		}
		// A foreign pane with a Pi that may still be alive is not touched.
		v.BridgeDead = false
		if ok, _ := agentRecoverDecision(v, cutoff, false); ok {
			t.Errorf("%s: foreign pane but bridge not confirmed dead must not relaunch", state)
		}
	}
}

func TestRecoverSkipsSessionWhoseWebViewerSurvives(t *testing.T) {
	f := newRecoverFake()
	sessions := map[string]*session.Session{"s": hostSess(f, "s", recoverNow)}
	f.live["s-web"] = true
	plan, err := planRecover(sessions, f.deps(), recoverOpts())
	if err != nil {
		t.Fatal(err)
	}
	if it := findItem(t, plan, "s"); it.Tmux != recoverActSkip || !strings.Contains(it.Reason, "s-web") {
		t.Fatalf("%+v", it)
	}
}

func TestRecoverLeavesSessionCreatedAfterPlanning(t *testing.T) {
	f := newRecoverFake()
	sessions := map[string]*session.Session{"s": hostSess(f, "s", recoverNow), "u": hostSess(f, "u", recoverNow)}
	plan, err := planRecover(sessions, f.deps(), recoverOpts())
	if err != nil {
		t.Fatal(err)
	}
	f.liveAfter = map[string]bool{"s": true}
	executeRecover(plan, sessions, f.deps(), recoverOpts())
	if strings.Join(f.ensured, ",") != "u" {
		t.Fatalf("ensured %v; a session that appeared since planning must be left alone", f.ensured)
	}
	if it := findItem(t, plan, "s"); it.Outcome != recoverOutSkipped {
		t.Fatalf("%+v", it)
	}
	// tmux unreachable at execution: nothing is recreated, reported as failure.
	f = newRecoverFake()
	sessions = map[string]*session.Session{"s": hostSess(f, "s", recoverNow)}
	plan, _ = planRecover(sessions, f.deps(), recoverOpts())
	f.liveErr = errors.New("permission denied")
	executeRecover(plan, sessions, f.deps(), recoverOpts())
	if len(f.ensured) != 0 || recoverFailures(plan) != 1 {
		t.Fatalf("ensured=%v failures=%d", f.ensured, recoverFailures(plan))
	}
}

func TestRecoverRelaunchedSincePlanningIsSkipped(t *testing.T) {
	f := newRecoverFake()
	sessions := map[string]*session.Session{"lo": loSess(f, "lo", "pa_111111111111", recoverNow)}
	f.views = []*piagent.AgentView{crashedAgent("pa_111111111111", "lo", recoverNow)}
	plan, err := planRecover(sessions, f.deps(), recoverOpts())
	if err != nil {
		t.Fatal(err)
	}
	// Another recover relaunched it, and that Pi already crashed again: it
	// still looks crashed, but the launch count moved.
	again := crashedAgent("pa_111111111111", "lo", recoverNow)
	again.Agent.LaunchCount = 1
	f.status = map[string]*piagent.AgentView{"pa_111111111111": again}
	executeRecover(plan, sessions, f.deps(), recoverOpts())
	if len(f.relaunch) != 0 {
		t.Fatalf("relaunched %v", f.relaunch)
	}
	// The manager's own launch-count check is honored as a skip, not a failure.
	f = newRecoverFake()
	sessions = map[string]*session.Session{"lo": loSess(f, "lo", "pa_111111111111", recoverNow)}
	f.views = []*piagent.AgentView{crashedAgent("pa_111111111111", "lo", recoverNow)}
	f.relErr["pa_111111111111"] = fmt.Errorf("x: %w", piagent.ErrLaunchedSinceCheck)
	plan, _ = planRecover(sessions, f.deps(), recoverOpts())
	executeRecover(plan, sessions, f.deps(), recoverOpts())
	if recoverFailures(plan) != 0 || plan.Items[0].Agents[0].Outcome != recoverOutSkipped {
		t.Fatalf("%+v", plan.Items[0].Agents)
	}
}

func TestRecoverPolicyErrorAndUnmatchedAgents(t *testing.T) {
	f := newRecoverFake()
	sessions := map[string]*session.Session{"lo": loSess(f, "lo", "pa_111111111111", recoverNow)}
	f.views = []*piagent.AgentView{
		crashedAgent("pa_111111111111", "lo", recoverNow),
		crashedAgent("pa_222222222222", "removed-session", recoverNow),
		crashedAgent("pa_333333333333", "lo", recoverNow),
	}
	f.peekErr["pa_333333333333"] = errors.New("corrupt agent.json")
	f.unreadable = map[string]error{"pa_444444444444": errors.New("unexpected end of JSON input")}
	f.policyErr = errors.New("launch policy unusable")
	plan, err := planRecover(sessions, f.deps(), recoverOpts())
	if err != nil {
		t.Fatalf("one unreadable agent must not block the rest: %v", err)
	}
	if a := findItem(t, plan, "lo").Agents[0]; a.Action != recoverActSkip || !strings.Contains(a.Reason, "launch policy") {
		t.Fatalf("policy error must be shown in the plan: %+v", a)
	}
	if len(plan.Unmatched) != 3 {
		t.Fatalf("unmatched %+v", plan.Unmatched)
	}
	if recoverFailures(plan) != 2 {
		t.Fatalf("unreadable agent must count as a failure: %d", recoverFailures(plan))
	}
	var buf bytes.Buffer
	printRecoverPlan(&buf, plan)
	for _, want := range []string{"pa_222222222222", "session record is gone", "corrupt agent.json", "pa_444444444444", "unexpected end of JSON"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output missing %q:\n%s", want, buf.String())
		}
	}
}

// tmux is re-queried before every recreation, so a session (or its viewer)
// that appears while an earlier session is being recreated is left alone.
func TestRecoverRequeriesTmuxBeforeEachRecreation(t *testing.T) {
	f := newRecoverFake()
	sessions := map[string]*session.Session{"a": hostSess(f, "a", recoverNow), "b": hostSess(f, "b", recoverNow)}
	plan, err := planRecover(sessions, f.deps(), recoverOpts())
	if err != nil {
		t.Fatal(err)
	}
	d := f.deps()
	ensure := d.ensureTmux
	d.ensureTmux = func(name string, s *session.Session) error {
		if name == "a" {
			f.live["b-web"] = true // b's viewer appears while a is recreated
		}
		return ensure(name, s)
	}
	executeRecover(plan, sessions, d, recoverOpts())
	if strings.Join(f.ensured, ",") != "a" {
		t.Fatalf("ensured %v", f.ensured)
	}
}

// The real ownership rule is applied: an agent bound to an earlier instance
// of its session is not relaunched into a recreated one.
func TestRecoverAppliesInstanceBinding(t *testing.T) {
	f := newRecoverFake()
	sess := loSess(f, "lo", "pa_111111111111", recoverNow)
	sess.InstanceID = "inst-new"
	v := crashedAgent("pa_111111111111", "lo", recoverNow)
	v.Agent.SessionInstanceID = "inst-old"
	f.views = []*piagent.AgentView{v}
	plan, err := planRecover(map[string]*session.Session{"lo": sess}, f.deps(), recoverOpts())
	if err != nil {
		t.Fatal(err)
	}
	if a := findItem(t, plan, "lo").Agents[0]; a.Action != recoverActSkip || !strings.Contains(a.Reason, "instance") {
		t.Fatalf("%+v", a)
	}
}

// Naming sessions limits agent reporting to them: an unrelated broken agent
// does not make `recover <name>` fail.
func TestRecoverNamedIgnoresUnrelatedAgents(t *testing.T) {
	f := newRecoverFake()
	sessions := map[string]*session.Session{"a": hostSess(f, "a", recoverNow), "lo": loSess(f, "lo", "pa_111111111111", recoverNow)}
	f.views = []*piagent.AgentView{crashedAgent("pa_111111111111", "lo", recoverNow)}
	f.peekErr["pa_111111111111"] = errors.New("boom")
	f.unreadable = map[string]error{"pa_999999999999": errors.New("corrupt")}
	opts := recoverOpts()
	opts.Names = []string{"a"}
	plan, err := planRecover(sessions, f.deps(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unmatched) != 0 || recoverFailures(plan) != 0 {
		t.Fatalf("unrelated agents reported: %+v", plan.Unmatched)
	}
}

// A surviving -web viewer also blocks relaunching the session's agents: the
// relaunch would create a new session the viewer cannot see.
func TestRecoverWebViewerBlocksAgentRelaunch(t *testing.T) {
	f := newRecoverFake()
	sessions := map[string]*session.Session{"lo": loSess(f, "lo", "pa_111111111111", recoverNow)}
	f.views = []*piagent.AgentView{crashedAgent("pa_111111111111", "lo", recoverNow)}
	f.live["lo-web"] = true
	plan, err := planRecover(sessions, f.deps(), recoverOpts())
	if err != nil {
		t.Fatal(err)
	}
	if a := findItem(t, plan, "lo").Agents[0]; a.Action != recoverActSkip || !strings.Contains(a.Reason, "web viewer") {
		t.Fatalf("%+v", a)
	}
	// Viewer appears between plan and execute.
	f = newRecoverFake()
	sessions = map[string]*session.Session{"lo": loSess(f, "lo", "pa_111111111111", recoverNow)}
	f.views = []*piagent.AgentView{crashedAgent("pa_111111111111", "lo", recoverNow)}
	plan, _ = planRecover(sessions, f.deps(), recoverOpts())
	f.live["lo-web"] = true
	executeRecover(plan, sessions, f.deps(), recoverOpts())
	if len(f.relaunch) != 0 || plan.Items[0].Agents[0].Outcome != recoverOutSkipped {
		t.Fatalf("relaunched %v: %+v", f.relaunch, plan.Items[0].Agents)
	}
}
