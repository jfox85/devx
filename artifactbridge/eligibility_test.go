package artifactbridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
)

// eligibilityFixture is one agent bound to one session instance, plus
// helpers to mutate either side. The default fixture is an agent bound by
// instance id to a session without a marker (for example an adopted session
// whose marker an older writer dropped), which must be eligible.
type eligibilityFixture struct {
	agent    *piagent.Agent
	sessions map[string]*session.Session
	agents   []*piagent.Agent
}

var fixtureT0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

const fixtureInstance = "si_aaaaaaaaaaaaaaaaaaaaaaaa"

func newEligibilityFixture() *eligibilityFixture {
	sessCreated := fixtureT0.Add(-200 * time.Millisecond)
	a := &piagent.Agent{ID: "pa_aaaaaaaaaaaa", DevxSession: "s1", Project: "proj", Worktree: wtPath("s1"), PiSessionID: "x", CreatedAt: fixtureT0,
		SessionInstanceID: fixtureInstance, SessionCreatedAt: sessCreated}
	return &eligibilityFixture{
		agent:    a,
		sessions: map[string]*session.Session{"s1": {Name: "s1", ProjectAlias: "proj", Path: wtPath("s1"), CreatedAt: sessCreated, InstanceID: fixtureInstance}},
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
		o := &piagent.Agent{ID: "pa_bbbbbbbbbbbb", DevxSession: "s2", Project: "proj", Worktree: wtPath("s2"), PiSessionID: "y"}
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
		{"trailing slash in path", func(f *eligibilityFixture) { f.sessions["s1"].Path = wtPath("s1") + string(filepath.Separator) }, ""},
		{"retired duplicate claimant ignored", func(f *eligibilityFixture) {
			f.agents = append(f.agents, other(func(o *piagent.Agent) { o.DevxSession, o.Worktree, o.RetiredAt = "s1", wtPath("s1"), &now }))
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
		{"session path moved", func(f *eligibilityFixture) { f.sessions["s1"].Path = wtPath("elsewhere") }, denyPathMismatch},
		{"agent worktree moved", func(f *eligibilityFixture) { f.agent.Worktree = wtPath("elsewhere") }, denyPathMismatch},
		{"relative session path", func(f *eligibilityFixture) { f.sessions["s1"].Path = "wt/s1" }, denyPathMismatch},
		{"relative agent worktree", func(f *eligibilityFixture) { f.agent.Worktree = "wt/s1" }, denyPathMismatch},
		{"empty session path", func(f *eligibilityFixture) { f.sessions["s1"].Path = "" }, denyPathMismatch},
		{"parent-of path", func(f *eligibilityFixture) { f.sessions["s1"].Path = filepath.Dir(wtPath("s1")) }, denyPathMismatch},

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

		// Session instance binding (identity, not name/path/timestamps).
		{"bound agent, same instance, no marker", func(f *eligibilityFixture) {}, ""},
		{"bound agent, same instance, session much older than agent (adopted)", func(f *eligibilityFixture) {
			f.sessions["s1"].CreatedAt = fixtureT0.Add(-30 * 24 * time.Hour)
			f.agent.SessionCreatedAt = f.sessions["s1"].CreatedAt
		}, ""},
		{"recreated session (new instance id), timestamps look older (clock rollback)", func(f *eligibilityFixture) {
			f.sessions["s1"].InstanceID = "si_bbbbbbbbbbbbbbbbbbbbbbbb"
			f.sessions["s1"].CreatedAt = fixtureT0.Add(-time.Hour)
		}, denyInstanceMismatch},
		{"recreated session (new instance id), restored record with the old created_at", func(f *eligibilityFixture) {
			f.sessions["s1"].InstanceID = "si_bbbbbbbbbbbbbbbbbbbbbbbb"
		}, denyInstanceMismatch},
		{"recreated session even with a marker naming the agent", func(f *eligibilityFixture) {
			f.sessions["s1"].InstanceID = "si_bbbbbbbbbbbbbbbbbbbbbbbb"
			f.sessions["s1"].ManagedAgent = f.agent.ID
		}, denyInstanceMismatch},
		{"instance id dropped by an older writer, same record (created_at exact)", func(f *eligibilityFixture) {
			f.sessions["s1"].InstanceID = ""
		}, ""},
		{"instance id missing and created_at differs by 1ns (recreated by an old writer)", func(f *eligibilityFixture) {
			f.sessions["s1"].InstanceID = ""
			f.sessions["s1"].CreatedAt = f.agent.SessionCreatedAt.Add(time.Nanosecond)
		}, denyInstanceMissing},
		{"instance id missing and created_at differs (clock rolled back)", func(f *eligibilityFixture) {
			f.sessions["s1"].InstanceID = ""
			f.sessions["s1"].CreatedAt = f.agent.SessionCreatedAt.Add(-time.Hour)
		}, denyInstanceMissing},
		{"instance id missing, agent has no recorded created_at", func(f *eligibilityFixture) {
			f.sessions["s1"].InstanceID = ""
			f.agent.SessionCreatedAt = time.Time{}
		}, denyInstanceMissing},
		{"timezone offsets do not matter for the created_at witness", func(f *eligibilityFixture) {
			f.sessions["s1"].InstanceID = ""
			f.sessions["s1"].CreatedAt = f.agent.SessionCreatedAt.In(time.FixedZone("PDT", -7*3600))
		}, ""},
		{"unbound legacy agent, no marker", func(f *eligibilityFixture) {
			f.agent.SessionInstanceID, f.agent.SessionCreatedAt = "", time.Time{}
		}, denyUnboundLegacy},
		{"unbound legacy agent, no marker, session has an id", func(f *eligibilityFixture) {
			f.agent.SessionInstanceID, f.agent.SessionCreatedAt = "", time.Time{}
			f.sessions["s1"].InstanceID = "si_cccccccccccccccccccccccc"
		}, denyUnboundLegacy},
		{"unbound legacy agent with managed marker naming it", func(f *eligibilityFixture) {
			f.agent.SessionInstanceID, f.agent.SessionCreatedAt = "", time.Time{}
			f.sessions["s1"].InstanceID = ""
			f.sessions["s1"].ManagedAgent = f.agent.ID
		}, ""},
		{"unbound legacy agent with local-only marker naming it", func(f *eligibilityFixture) {
			f.agent.SessionInstanceID, f.agent.SessionCreatedAt = "", time.Time{}
			f.sessions["s1"].InstanceID = ""
			f.sessions["s1"].LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: f.agent.ID}
		}, ""},
		{"unbound legacy agent, marker re-written on a RECREATED (newer) session", func(f *eligibilityFixture) {
			f.agent.SessionInstanceID, f.agent.SessionCreatedAt = "", time.Time{}
			f.sessions["s1"].InstanceID = ""
			f.sessions["s1"].ManagedAgent = f.agent.ID
			f.sessions["s1"].CreatedAt = fixtureT0.Add(time.Hour)
		}, denyUnboundLegacy},
		{"unbound legacy agent, marker on a session that already has an id", func(f *eligibilityFixture) {
			f.agent.SessionInstanceID, f.agent.SessionCreatedAt = "", time.Time{}
			f.sessions["s1"].ManagedAgent = f.agent.ID
		}, denyUnboundLegacy},

		// Duplicate claimants and shared worktrees.
		{"live agent claims same session", func(f *eligibilityFixture) {
			f.agents = append(f.agents, other(func(o *piagent.Agent) { o.DevxSession = "s1" }))
		}, denyDuplicateClaim},
		{"live agent claims same session even with our marker", func(f *eligibilityFixture) {
			f.sessions["s1"].ManagedAgent = f.agent.ID
			f.agents = append(f.agents, other(func(o *piagent.Agent) { o.DevxSession = "s1" }))
		}, denyDuplicateClaim},
		{"live agent of another session uses same worktree", func(f *eligibilityFixture) {
			f.agents = append(f.agents, other(func(o *piagent.Agent) { o.Worktree = wtPath("s1") }))
		}, denySharedWorktree},
		{"another session record uses same worktree", func(f *eligibilityFixture) {
			f.sessions["s2"] = &session.Session{Name: "s2", ProjectAlias: "proj", Path: wtPath("s1") + string(filepath.Separator)}
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
	ls.ManagedAgent = "" // bound by instance id; marker dropped: still allowed
	unbound, us := e.agent("unbound", "proj")
	unbound.SessionInstanceID, unbound.SessionCreatedAt = "", time.Time{}
	_ = e.store.WithAgentLock(unbound.ID, func() error { return e.store.SaveAgent(unbound) })
	us.ManagedAgent, us.InstanceID = "", "" // legacy agent, no marker: needs migration
	unboundMarked, ums := e.agent("marked-legacy", "proj")
	unboundMarked.SessionInstanceID, unboundMarked.SessionCreatedAt = "", time.Time{}
	_ = e.store.WithAgentLock(unboundMarked.ID, func() error { return e.store.SaveAgent(unboundMarked) })
	ums.InstanceID = "" // legacy agent, marker names it: allowed
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
	want := map[string]string{"ok": "allowed", "legacy": "allowed", "unbound": denyUnboundLegacy, "marked-legacy": "allowed", "conflict": denyForeignManaged,
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
	// Worst case for timestamps: the clock was rolled back, so the new
	// record looks OLDER than the agent; and the human even sets a marker
	// naming the old agent id by hand. The instance id still differs.
	e.sessions["feat"] = &session.Session{Name: "feat", ProjectAlias: "proj", Path: a.Worktree,
		CreatedAt: e.now.Add(-24 * time.Hour), InstanceID: session.NewInstanceID()}
	e.register(e.sessions["feat"], "Human", "human.md", []byte("private"))
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("recreated session must not be exposed via the orphaned agent: %v", err)
	}
	e.sessions["feat"].ManagedAgent = a.ID
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("a marker naming the old agent must not rebind a recreated session: %v", err)
	}
	e.sessions["feat"].ManagedAgent = ""
	// Recreated by an OLD writer that sets no instance id, with the same
	// (rolled-back) created_at second but a different instant: denied.
	e.sessions["feat"].InstanceID = ""
	e.sessions["feat"].CreatedAt = a.SessionCreatedAt.Add(time.Microsecond)
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("id-less recreated record must not match: %v", err)
	}
	// Not even when explicitly listed: the binding itself is invalid.
	e.pol.Sessions, e.pol.SessionsSet = []string{"feat"}, true
	if _, err := e.svc.List(ListRequest{AgentID: a.ID}); codeOf(err) != codeDenied {
		t.Fatalf("explicit list must not revive an orphaned agent: %v", err)
	}
}

// wtPath returns an absolute worktree path for test fixtures on every
// platform ("/wt/<name>" on Unix, "<volume>\\wt\\<name>" on Windows): the
// code under test requires absolute paths, and "/wt/x" is not absolute on
// Windows.
func wtPath(name string) string {
	root := string(filepath.Separator)
	if v := filepath.VolumeName(os.TempDir()); v != "" {
		root = v + root
	}
	return filepath.Join(root, "wt", name)
}
