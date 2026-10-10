package artifactbridge

import (
	"testing"
	"time"

	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
)

// eligibilityFixture is one agent bound to one session, plus helpers to
// mutate either side. The default fixture is a legacy/adopted-style session
// with no marker, which must be eligible.
type eligibilityFixture struct {
	agent    *piagent.Agent
	sessions map[string]*session.Session
	agents   []*piagent.Agent
}

var fixtureT0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newEligibilityFixture() *eligibilityFixture {
	a := &piagent.Agent{ID: "pa_aaaaaaaaaaaa", DevxSession: "s1", Project: "proj", Worktree: "/wt/s1", PiSessionID: "x", CreatedAt: fixtureT0}
	return &eligibilityFixture{
		agent:    a,
		sessions: map[string]*session.Session{"s1": {Name: "s1", ProjectAlias: "proj", Path: "/wt/s1", CreatedAt: fixtureT0.Add(-200 * time.Millisecond)}},
		agents:   []*piagent.Agent{a},
	}
}

func (f *eligibilityFixture) check() string {
	_, reason := eligibleSession(f.agent, f.sessions, f.agents)
	return reason
}

func TestEligibleSessionPredicate(t *testing.T) {
	now := time.Now()
	other := func(mod func(o *piagent.Agent)) *piagent.Agent {
		o := &piagent.Agent{ID: "pa_bbbbbbbbbbbb", DevxSession: "s2", Project: "proj", Worktree: "/wt/s2", PiSessionID: "y"}
		mod(o)
		return o
	}
	cases := []struct {
		name string
		mod  func(f *eligibilityFixture)
		want string
	}{
		// Positive cases.
		{"no marker (legacy MCP-created / adopted marker dropped)", func(f *eligibilityFixture) {}, ""},
		{"managed marker names agent", func(f *eligibilityFixture) { f.sessions["s1"].ManagedAgent = f.agent.ID }, ""},
		{"local-only marker names agent", func(f *eligibilityFixture) {
			f.sessions["s1"].LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: f.agent.ID}
		}, ""},
		{"session without project alias", func(f *eligibilityFixture) { f.sessions["s1"].ProjectAlias = "" }, ""},
		{"trailing slash in path", func(f *eligibilityFixture) { f.sessions["s1"].Path = "/wt/s1/" }, ""},
		{"retired duplicate claimant ignored", func(f *eligibilityFixture) {
			f.agents = append(f.agents, other(func(o *piagent.Agent) { o.DevxSession, o.Worktree, o.RetiredAt = "s1", "/wt/s1", &now }))
		}, ""},
		{"unrelated live agent", func(f *eligibilityFixture) { f.agents = append(f.agents, other(func(*piagent.Agent) {})) }, ""},

		// Agent record problems.
		{"retired agent", func(f *eligibilityFixture) { f.agent.RetiredAt = &now }, denyRetired},
		{"agent without project", func(f *eligibilityFixture) { f.agent.Project = "" }, denyRetired},
		{"agent without session", func(f *eligibilityFixture) { f.agent.DevxSession = "" }, denyRetired},

		// Stale / removed session records.
		{"session removed", func(f *eligibilityFixture) { delete(f.sessions, "s1") }, denyNoSession},
		{"nil session record", func(f *eligibilityFixture) { f.sessions["s1"] = nil }, denyNoSession},
		{"record name differs from key", func(f *eligibilityFixture) { f.sessions["s1"].Name = "s1-renamed" }, denyNoSession},

		// Path changes.
		{"session path moved", func(f *eligibilityFixture) { f.sessions["s1"].Path = "/wt/elsewhere" }, denyPathMismatch},
		{"agent worktree moved", func(f *eligibilityFixture) { f.agent.Worktree = "/wt/elsewhere" }, denyPathMismatch},
		{"relative session path", func(f *eligibilityFixture) { f.sessions["s1"].Path = "wt/s1" }, denyPathMismatch},
		{"relative agent worktree", func(f *eligibilityFixture) { f.agent.Worktree = "wt/s1" }, denyPathMismatch},
		{"empty session path", func(f *eligibilityFixture) { f.sessions["s1"].Path = "" }, denyPathMismatch},
		{"parent-of path", func(f *eligibilityFixture) { f.sessions["s1"].Path = "/wt" }, denyPathMismatch},

		// Project mismatch.
		{"session project differs", func(f *eligibilityFixture) { f.sessions["s1"].ProjectAlias = "other" }, denyProjectMismatch},

		// Ownership conflicts (never overridden).
		{"managed marker names other agent", func(f *eligibilityFixture) { f.sessions["s1"].ManagedAgent = "pa_bbbbbbbbbbbb" }, denyForeignManaged},
		{"local-only marker names other agent", func(f *eligibilityFixture) {
			f.sessions["s1"].LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: "pa_bbbbbbbbbbbb"}
		}, denyForeignLocalOnly},
		{"local-only marker with empty agent", func(f *eligibilityFixture) {
			f.sessions["s1"].LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP}
		}, denyForeignLocalOnly},
		{"both markers, both this agent", func(f *eligibilityFixture) {
			f.sessions["s1"].ManagedAgent = f.agent.ID
			f.sessions["s1"].LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: f.agent.ID}
		}, denyBothMarkers},
		{"container target", func(f *eligibilityFixture) { f.sessions["s1"].Target = session.TargetMeta{Type: "docker"} }, denyContainerized},

		// Session instance binding for unmarked sessions.
		{"adopted: unmarked session much older than agent", func(f *eligibilityFixture) {
			f.sessions["s1"].CreatedAt = fixtureT0.Add(-30 * 24 * time.Hour)
		}, ""},
		{"unmarked session created at the same instant as agent", func(f *eligibilityFixture) {
			f.sessions["s1"].CreatedAt = fixtureT0
		}, ""},
		{"unmarked session recreated 1ns after agent", func(f *eligibilityFixture) {
			f.sessions["s1"].CreatedAt = fixtureT0.Add(time.Nanosecond)
		}, denyNewerSession},
		{"unmarked session recreated 30s after agent (within old 1-minute window)", func(f *eligibilityFixture) {
			f.sessions["s1"].CreatedAt = fixtureT0.Add(30 * time.Second)
		}, denyNewerSession},
		{"unmarked session recreated after agent (orphaned agent)", func(f *eligibilityFixture) {
			f.sessions["s1"].CreatedAt = fixtureT0.Add(2 * time.Hour)
		}, denyNewerSession},
		{"timezone offsets do not matter", func(f *eligibilityFixture) {
			f.sessions["s1"].CreatedAt = fixtureT0.Add(-time.Millisecond).In(time.FixedZone("PDT", -7*3600))
		}, ""},
		{"unmarked session with zero created time", func(f *eligibilityFixture) { f.sessions["s1"].CreatedAt = time.Time{} }, denyNewerSession},
		{"unmarked session, agent with zero created time", func(f *eligibilityFixture) { f.agent.CreatedAt = time.Time{} }, denyNewerSession},
		{"marked session newer than agent is fine (marker binds instance)", func(f *eligibilityFixture) {
			f.sessions["s1"].CreatedAt = fixtureT0.Add(2 * time.Hour)
			f.sessions["s1"].ManagedAgent = f.agent.ID
		}, ""},

		// Duplicate claimants and shared worktrees.
		{"live agent claims same session", func(f *eligibilityFixture) {
			f.agents = append(f.agents, other(func(o *piagent.Agent) { o.DevxSession = "s1" }))
		}, denyDuplicateClaim},
		{"live agent claims same session even with our marker", func(f *eligibilityFixture) {
			f.sessions["s1"].ManagedAgent = f.agent.ID
			f.agents = append(f.agents, other(func(o *piagent.Agent) { o.DevxSession = "s1" }))
		}, denyDuplicateClaim},
		{"live agent of another session uses same worktree", func(f *eligibilityFixture) {
			f.agents = append(f.agents, other(func(o *piagent.Agent) { o.Worktree = "/wt/s1" }))
		}, denySharedWorktree},
		{"another session record uses same worktree", func(f *eligibilityFixture) {
			f.sessions["s2"] = &session.Session{Name: "s2", ProjectAlias: "proj", Path: "/wt/s1/"}
		}, denySharedWorktree},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newEligibilityFixture()
			c.mod(f)
			if got := f.check(); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

// Agent state (pending adoption, human control, running, ...) is not part of
// eligibility, but every structural check still applies to such agents.
func TestEligibilityIgnoresAgentStateButNotStructure(t *testing.T) {
	e := newEnv(t)
	e.defaultScope()
	a, s := e.agent("pending", "proj")
	e.register(s, "One", "one.md", []byte("one"))
	// adopted_pending_relaunch / human control: lease held by human, no bridge.
	a.Adopted = true
	a.Lease = piagent.Lease{Holder: piagent.LeaseHuman, Generation: 1, Reason: "adopted"}
	_ = e.store.WithAgentLock(a.ID, func() error { return e.store.SaveAgent(a) })
	s.ManagedAgent = "" // and its adoption marker was dropped by an old writer
	if len(e.list(a.ID)) != 1 {
		t.Fatal("pending-adoption agent with valid structure should be readable")
	}
	// Same pending agent, now a stale record: worktree moved.
	s.Path = s.Path + "-moved"
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("stale pending agent: %v", err)
	}
}

func TestReadInventoryUsesSamePredicateAndPolicy(t *testing.T) {
	e := newEnv(t)
	e.defaultScope()
	ok, _ := e.agent("ok", "proj")
	legacy, ls := e.agent("legacy", "proj")
	ls.ManagedAgent = ""
	_, cs := e.agent("conflict", "proj")
	cs.ManagedAgent = "pa_ffffffffffff"
	_, _ = e.agent("excluded", "proj")
	_, _ = e.agent("otherproj", "forbidden")
	stale, _ := e.agent("stale", "proj")
	delete(e.sessions, "stale")
	e.pol.ExcludeSessions = []string{"excluded"}
	inv, err := e.svc.ReadInventory()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range inv {
		if r.Allowed {
			got[r.Session] = "allowed"
		} else {
			got[r.Session] = r.Reason
		}
	}
	want := map[string]string{"ok": "allowed", "legacy": "allowed", "conflict": denyForeignManaged,
		"excluded": "policy", "otherproj": "policy", "stale": denyNoSession}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: got %q want %q (all: %v)", k, got[k], v, got)
		}
	}
	// Inventory agrees with authorize for every agent.
	for _, r := range inv {
		_, err := e.svc.authorize(r.AgentID, capRead)
		if (err == nil) != r.Allowed {
			t.Fatalf("%s: inventory %v, authorize err=%v", r.Session, r.Allowed, err)
		}
	}
	_, _ = ok, legacy
	_ = stale
	// Unreadable policy: inventory fails closed too.
	e.polErr = errPolicy
	if _, err := e.svc.ReadInventory(); err == nil {
		t.Fatal("inventory must fail closed on bad policy")
	}
}

// Explicit deny rules and project removal still apply to marker-less sessions.
func TestMarkerlessSessionsHonorPolicy(t *testing.T) {
	e := newEnv(t)
	e.defaultScope()
	a, s := e.agent("legacy", "proj")
	s.ManagedAgent = ""
	e.register(s, "One", "one.md", []byte("one"))
	for name, mod := range map[string]func(){
		"exclude_sessions": func() { e.pol.ExcludeSessions = []string{"legacy"} },
		"exclude_projects": func() { e.pol.ExcludeProjects = []string{"proj"} },
		"project removed":  func() { e.pol.AllowedProjects = []string{"other"} },
		"explicit list":    func() { e.pol.Sessions, e.pol.SessionsSet = []string{"someone-else"}, true },
		"read off":         func() { e.pol.Read = false },
	} {
		saved := e.pol
		mod()
		if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
			t.Fatalf("%s: %v", name, err)
		}
		e.pol = saved
	}
	// Upload is never available through the default scope, marker or not.
	e.pol.Upload = true
	if _, err := e.svc.Upload(uploadReq(a.ID, "k", "n.txt", "text/plain", []byte("x"), 0, true)); codeOf(err) != codeDenied {
		t.Fatalf("upload via default scope: %v", err)
	}
}

// Review finding: a session removed without retiring its agent (for example
// `devx session clear`, or `rm` of a session whose marker was lost) and then
// recreated by a human under the same deterministic name/path must not be
// readable through the orphaned agent.
func TestOrphanedAgentDoesNotInheritRecreatedSession(t *testing.T) {
	e := newEnv(t)
	e.defaultScope()
	a, s := e.agent("feat", "proj")
	s.ManagedAgent = "" // unmarked, as for legacy/adopted sessions
	e.register(s, "Old", "old.md", []byte("old"))
	if len(e.list(a.ID)) != 1 {
		t.Fatal("original unmarked session should be readable")
	}
	// Session removed (agent left unretired), then recreated later with the
	// same name and worktree path and a new artifact.
	delete(e.sessions, "feat")
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("removed session: %v", err)
	}
	e.sessions["feat"] = &session.Session{Name: "feat", ProjectAlias: "proj", Path: a.Worktree, CreatedAt: e.now.Add(3 * time.Hour)}
	e.register(e.sessions["feat"], "Human", "human.md", []byte("private"))
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("recreated session must not be exposed via the orphaned agent: %v", err)
	}
	// Not even when explicitly listed: the binding itself is invalid.
	e.pol.Sessions, e.pol.SessionsSet = []string{"feat"}, true
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("explicit list must not revive an orphaned agent: %v", err)
	}
}
