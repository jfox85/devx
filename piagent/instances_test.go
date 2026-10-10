package piagent

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jfox85/devx/session"
)

// memSessions is an in-memory session store with the same lock+replace
// semantics the real store has (Mutate re-reads the latest state).
type memSessions struct {
	mu   sync.Mutex
	recs map[string]*session.Session
	// failAfter, when > 0, makes the Nth MutateSessions write fail (crash).
	writes, failAt int
}

func (m *memSessions) clone() map[string]*session.Session {
	out := map[string]*session.Session{}
	for k, v := range m.recs {
		cp := *v
		out[k] = &cp
	}
	return out
}

func (m *memSessions) stores(agents *Store, journal *[]JournalEntry) InstanceStores {
	return InstanceStores{
		MutateSessions: func(fn func(map[string]*session.Session) error) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			work := m.clone()
			if err := fn(work); err != nil {
				return err
			}
			m.writes++
			if m.failAt > 0 && m.writes == m.failAt {
				return errors.New("simulated crash before sessions write")
			}
			m.recs = work
			return nil
		},
		LoadSessions: func() (map[string]*session.Session, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			return m.clone(), nil
		},
		Agents: agents,
		Journal: func(e JournalEntry) error {
			*journal = append(*journal, e)
			return nil
		},
	}
}

type instEnv struct {
	t      *testing.T
	store  *Store
	sess   *memSessions
	t0     time.Time
	agents map[string]*Agent
}

func newInstEnv(t *testing.T) *instEnv {
	return &instEnv{t: t, store: NewStore(filepath.Join(t.TempDir(), "state")), sess: &memSessions{recs: map[string]*session.Session{}},
		t0: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), agents: map[string]*Agent{}}
}

// legacy adds a pre-instance-id session (no id) and agent (no binding).
func (e *instEnv) legacy(name, marker string, sessOffset time.Duration) *Agent {
	e.t.Helper()
	id := fmt.Sprintf("pa_%012x", len(e.agents)+1)
	wt := "/wt/" + name
	s := &session.Session{Name: name, ProjectAlias: "proj", Path: wt, CreatedAt: e.t0.Add(sessOffset)}
	switch marker {
	case "local":
		s.LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: id}
	case "managed":
		s.ManagedAgent = id
	}
	e.sess.recs[name] = s
	a := &Agent{ID: id, DevxSession: name, Project: "proj", Worktree: wt, PiSessionID: "x", CreatedAt: e.t0}
	if err := e.store.ensureAgentDirs(id); err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.WithAgentLock(id, func() error { return e.store.SaveAgent(a) }); err != nil {
		e.t.Fatal(err)
	}
	e.agents[name] = a
	return a
}

func (e *instEnv) list() []*Agent {
	as, err := e.store.ListAgents()
	if err != nil {
		e.t.Fatal(err)
	}
	return as
}

func (e *instEnv) plan() *InstancePlan {
	return PlanInstances(e.sess.clone(), e.list(), func(n string) string {
		return session.DeriveInstanceID([]byte("0123456789abcdef0123456789abcdef"), n, e.sess.recs[n].Path, e.sess.recs[n].CreatedAt)
	})
}

func (p *InstancePlan) agent(id string) AgentPlanItem {
	for _, a := range p.Agents {
		if a.AgentID == id {
			return a
		}
	}
	return AgentPlanItem{}
}

func TestPlanBindsOnlyUnambiguousLegacyAgents(t *testing.T) {
	e := newInstEnv(t)
	ok1 := e.legacy("local", "local", -100*time.Millisecond)
	ok2 := e.legacy("adopted", "managed", -30*24*time.Hour)
	ok3 := e.legacy("pre-local-only", "", -100*time.Millisecond)
	newer := e.legacy("recreated", "", 2*time.Hour) // session newer than agent
	noTime := e.legacy("notime", "", 0)
	e.sess.recs["notime"].CreatedAt = time.Time{}
	foreign := e.legacy("foreign", "", -time.Second)
	e.sess.recs["foreign"].ManagedAgent = "pa_ffffffffffff"
	both := e.legacy("both", "local", -time.Second)
	e.sess.recs["both"].ManagedAgent = both.ID
	moved := e.legacy("moved", "local", -time.Second)
	e.sess.recs["moved"].Path = "/elsewhere"
	gone := e.legacy("gone", "local", -time.Second)
	delete(e.sess.recs, "gone")
	dupA := e.legacy("dup", "", -time.Second)
	dupB := &Agent{ID: "pa_dddddddddddd", DevxSession: "dup", Project: "proj", Worktree: "/wt/dup2", PiSessionID: "y", CreatedAt: e.t0}
	_ = e.store.ensureAgentDirs(dupB.ID)
	_ = e.store.WithAgentLock(dupB.ID, func() error { return e.store.SaveAgent(dupB) })
	retired := e.legacy("retired", "local", -time.Second)
	now := e.t0
	retired.RetiredAt = &now
	_ = e.store.WithAgentLock(retired.ID, func() error { return e.store.SaveAgent(retired) })
	shared := e.legacy("shared", "local", -time.Second)
	e.sess.recs["shared-twin"] = &session.Session{Name: "shared-twin", Path: "/wt/shared", CreatedAt: e.t0}

	p := e.plan()
	want := map[string]string{
		ok1.ID: ActionBindAgent, ok2.ID: ActionBindAgent, ok3.ID: ActionBindAgent,
		newer.ID: SkipSessionNewer, noTime.ID: SkipNoTimes, foreign.ID: SkipForeignMarker, both.ID: SkipBothMarkers,
		moved.ID: SkipPathMismatch, gone.ID: SkipNoSession, dupA.ID: SkipDuplicateClaim, dupB.ID: SkipPathMismatch,
		retired.ID: SkipRetired, shared.ID: SkipSharedWorktree,
	}
	for id, w := range want {
		it := p.agent(id)
		got := it.Action
		if got == ActionSkip {
			got = it.Reason
		}
		if got != w {
			t.Errorf("%s (%s): got %q want %q", id, it.Session, got, w)
		}
	}
	// Bound agents point at the id proposed for their own session.
	for _, it := range p.Agents {
		if it.Action != ActionBindAgent {
			continue
		}
		var sid string
		for _, s := range p.Sessions {
			if s.Session == it.Session {
				sid = s.InstanceID
			}
		}
		if sid == "" || sid != it.SessionInstanceID || !session.ValidInstanceID(sid) {
			t.Fatalf("%s bound to %q, session proposed %q", it.AgentID, it.SessionInstanceID, sid)
		}
	}
	if !p.VerifyHash() {
		t.Fatal("hash")
	}
	// Deterministic: same records -> same plan hash (reviewable).
	if p2 := e.plan(); p2.Hash != p.Hash {
		t.Fatal("plan hash must be stable for unchanged records")
	}
}

func TestApplyIdempotentResumableAndStaleSafe(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("one", "local", -time.Second)
	b := e.legacy("two", "", -time.Second)
	p := e.plan()
	var journal []JournalEntry

	// Crash during the sessions write: nothing written, re-plan identical.
	e.sess.failAt = 1
	if err := ApplyInstancePlan(p, e.sess.stores(e.store, &journal)); err == nil {
		t.Fatal("expected simulated crash")
	}
	if e.sess.recs["one"].InstanceID != "" {
		t.Fatal("crashed write must not be visible")
	}
	if e.plan().Hash != p.Hash {
		t.Fatal("after a crash before any write, the same plan applies")
	}
	e.sess.failAt = 0

	// Crash after sessions, before agents: simulate by applying, then
	// clearing one agent's binding as if its write never happened.
	journal = nil
	if err := ApplyInstancePlan(p, e.sess.stores(e.store, &journal)); err != nil {
		t.Fatal(err)
	}
	ga, _ := e.store.LoadAgent(b.ID)
	ga.SessionInstanceID, ga.SessionCreatedAt = "", time.Time{}
	_ = e.store.WithAgentLock(b.ID, func() error { return e.store.SaveAgent(ga) })
	p2 := e.plan()
	if p2.agent(b.ID).Action != ActionBindAgent || p2.agent(a.ID).Action != ActionBound {
		t.Fatalf("resume plan: %+v", p2.Agents)
	}
	for _, s := range p2.Sessions {
		if s.Action == ActionAssignSessionID {
			t.Fatalf("sessions already have ids, nothing to assign: %+v", s)
		}
	}
	if err := ApplyInstancePlan(p2, e.sess.stores(e.store, &journal)); err != nil {
		t.Fatal(err)
	}
	// Idempotent: a third plan has no work; re-applying an old plan is a no-op.
	if e.plan().HasWork() {
		t.Fatal("plan should be empty after apply")
	}
	if err := ApplyInstancePlan(p, e.sess.stores(e.store, &journal)); err != nil {
		t.Fatalf("re-applying an applied plan must be a no-op: %v", err)
	}
	fa, _ := e.store.LoadAgent(a.ID)
	if fa.SessionInstanceID != e.sess.recs["one"].InstanceID || !fa.SessionCreatedAt.Equal(e.sess.recs["one"].CreatedAt) {
		t.Fatal("binding wrong")
	}

	// Stale: a record changed after review -> refuse, write nothing more.
	e2 := newInstEnv(t)
	e2.legacy("x", "local", -time.Second)
	px := e2.plan()
	e2.sess.recs["x"].InstanceID = session.NewInstanceID() // concurrent writer
	var j2 []JournalEntry
	if err := ApplyInstancePlan(px, e2.sess.stores(e2.store, &j2)); !errors.Is(err, ErrPlanStale) {
		t.Fatalf("want ErrPlanStale, got %v", err)
	}
	// Tampered plan content is refused.
	px.Agents[0].SessionInstanceID = "si_ffffffffffffffffffffffff"
	if err := ApplyInstancePlan(px, e2.sess.stores(e2.store, &j2)); err == nil {
		t.Fatal("tampered plan must be refused")
	}
}

func TestApplyRefusesWhenSessionRecreatedBetweenReviewAndApply(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("feat", "", -time.Second)
	p := e.plan()
	// Session removed and recreated (same name and path) after review, with
	// the clock rolled back so it even looks older.
	e.sess.recs["feat"] = &session.Session{Name: "feat", ProjectAlias: "proj", Path: "/wt/feat", CreatedAt: e.t0.Add(-time.Hour)}
	var j []JournalEntry
	err := ApplyInstancePlan(p, e.sess.stores(e.store, &j))
	if !errors.Is(err, ErrPlanStale) {
		t.Fatalf("want ErrPlanStale, got %v", err)
	}
	if got, _ := e.store.LoadAgent(a.ID); got.SessionInstanceID != "" {
		t.Fatal("agent must not be bound to the recreated session")
	}
}

func TestOldWriterDroppedIDIsRestoredNotReplaced(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("s", "local", -time.Second)
	var j []JournalEntry
	if err := ApplyInstancePlan(e.plan(), e.sess.stores(e.store, &j)); err != nil {
		t.Fatal(err)
	}
	bound := e.sess.recs["s"].InstanceID
	// An old devx binary rewrites sessions.json and drops instance_id (and,
	// here, the marker too). created_at is preserved by old writers.
	e.sess.recs["s"].InstanceID = ""
	e.sess.recs["s"].LocalOnly = nil
	p := e.plan()
	if len(p.Sessions) != 1 || p.Sessions[0].InstanceID != bound || !p.Sessions[0].Existing {
		t.Fatalf("must restore the bound id, got %+v", p.Sessions)
	}
	if p.agent(a.ID).Action != ActionBound {
		t.Fatalf("agent stays bound: %+v", p.agent(a.ID))
	}
	if err := ApplyInstancePlan(p, e.sess.stores(e.store, &j)); err != nil {
		t.Fatal(err)
	}
	if e.sess.recs["s"].InstanceID != bound {
		t.Fatal("restored id differs")
	}
	// Old writer recreated it instead (new created_at): NOT restored.
	e.sess.recs["s"] = &session.Session{Name: "s", ProjectAlias: "proj", Path: "/wt/s", CreatedAt: e.t0.Add(-5 * time.Minute)}
	p = e.plan()
	if p.agent(a.ID).Reason != SkipBoundIDMissing {
		t.Fatalf("recreated id-less record must not inherit: %+v", p.agent(a.ID))
	}
	if p.Sessions[0].Existing || p.Sessions[0].InstanceID == bound {
		t.Fatal("recreated record must get a NEW id")
	}
}

func TestRollbackRemovesOnlyWhatMigrationWrote(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("a", "local", -time.Second)
	b := e.legacy("b", "local", -time.Second)
	// A session that already had an id before the migration.
	pre := session.NewInstanceID()
	e.sess.recs["pre"] = &session.Session{Name: "pre", Path: "/wt/pre", CreatedAt: e.t0, InstanceID: pre}
	var j []JournalEntry
	if err := ApplyInstancePlan(e.plan(), e.sess.stores(e.store, &j)); err != nil {
		t.Fatal(err)
	}
	// After the migration b's binding was changed by someone else.
	gb, _ := e.store.LoadAgent(b.ID)
	gb.SessionInstanceID = "si_111111111111111111111111"
	_ = e.store.WithAgentLock(b.ID, func() error { return e.store.SaveAgent(gb) })
	n, skipped, err := RollbackInstances(j, e.sess.stores(e.store, &[]JournalEntry{}))
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || len(skipped) != 1 { // a binding + 2 session ids; b skipped
		t.Fatalf("reverted=%d skipped=%v", n, skipped)
	}
	if ga, _ := e.store.LoadAgent(a.ID); ga.SessionInstanceID != "" || !ga.SessionCreatedAt.IsZero() {
		t.Fatal("a not reverted")
	}
	if e.sess.recs["a"].InstanceID != "" || e.sess.recs["pre"].InstanceID != pre {
		t.Fatal("rollback must remove only migration-written ids")
	}
	if e.sess.recs["a"].LocalOnly == nil || e.sess.recs["a"].LocalOnly.AgentID != a.ID {
		t.Fatal("markers must be untouched")
	}
	// Rollback is repeatable.
	if n2, _, err := RollbackInstances(j, e.sess.stores(e.store, &[]JournalEntry{})); err != nil || n2 != 0 {
		t.Fatalf("second rollback: %d %v", n2, err)
	}
}

func TestConcurrentApplyIsSafe(t *testing.T) {
	e := newInstEnv(t)
	for i := 0; i < 6; i++ {
		e.legacy(fmt.Sprintf("s%d", i), "local", -time.Second)
	}
	p := e.plan()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var j []JournalEntry
			err := ApplyInstancePlan(p, e.sess.stores(e.store, &j))
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent apply of the same plan must converge: %v", err)
		}
	}
	if e.plan().HasWork() {
		t.Fatal("not fully applied")
	}
	for name, a := range e.agents {
		got, _ := e.store.LoadAgent(a.ID)
		if got.SessionInstanceID != e.sess.recs[name].InstanceID {
			t.Fatalf("%s mismatched", name)
		}
	}
}

// P1-b: a marker is not proof. Before instance ids, relaunching an adopted
// agent re-marked whatever session had the name, so a stale adopted agent can
// carry a marker on a RECREATED session. Ordering applies to every marker.
func TestPlanSkipsMarkedButRecreatedSession(t *testing.T) {
	e := newInstEnv(t)
	adopted := e.legacy("adopted", "managed", 3*time.Hour) // re-marked recreation
	local := e.legacy("local", "local", 3*time.Hour)
	p := e.plan()
	if it := p.agent(adopted.ID); it.Reason != SkipSessionNewer {
		t.Fatalf("adopted: %+v", it)
	}
	if it := p.agent(local.ID); it.Reason != SkipSessionNewer {
		t.Fatalf("local: %+v", it)
	}
}

// P2-1: an unbound legacy agent never binds to a record that already has an
// id this migration didn't derive (created by an instance-aware build, i.e.
// after the agent), even if the clock was rolled back so it looks older.
func TestPlanSkipsUnboundAgentWhenSessionAlreadyHasForeignID(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("s", "", -time.Hour) // looks older (rolled-back clock)
	e.sess.recs["s"].InstanceID = session.NewInstanceID()
	if it := e.plan().agent(a.ID); it.Reason != SkipSessionHasID {
		t.Fatalf("got %+v", it)
	}
}

// P1-c: the session id is written only to the exact reviewed record. A
// recreation between review and apply aborts BEFORE any session id is
// written, including the restore of a dropped id.
func TestApplyWritesSessionIDOnlyToReviewedRecord(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("feat", "local", -time.Second)
	var j []JournalEntry
	if err := ApplyInstancePlan(e.plan(), e.sess.stores(e.store, &j)); err != nil {
		t.Fatal(err)
	}
	bound := e.sess.recs["feat"].InstanceID
	// Old writer drops the id (same record): plan restores `bound`.
	e.sess.recs["feat"].InstanceID = ""
	p := e.plan()
	if len(p.Sessions) != 1 || !p.Sessions[0].Existing || p.Sessions[0].InstanceID != bound {
		t.Fatalf("restore plan: %+v", p.Sessions)
	}
	// Before apply, an old binary removes and recreates the session (no id,
	// new created_at, same path).
	e.sess.recs["feat"] = &session.Session{Name: "feat", ProjectAlias: "proj", Path: "/wt/feat", CreatedAt: e.t0.Add(-time.Hour)}
	if err := ApplyInstancePlan(p, e.sess.stores(e.store, &j)); !errors.Is(err, ErrPlanStale) {
		t.Fatalf("want ErrPlanStale, got %v", err)
	}
	if e.sess.recs["feat"].InstanceID != "" {
		t.Fatal("the recreated record must not receive the old instance id")
	}
	if got, _ := e.store.LoadAgent(a.ID); got.SessionInstanceID != bound {
		t.Fatal("agent binding unchanged")
	}
	// Fresh-id assignment is likewise bound to the reviewed record.
	e2 := newInstEnv(t)
	e2.legacy("x", "local", -time.Second)
	px := e2.plan()
	e2.sess.recs["x"].CreatedAt = e2.sess.recs["x"].CreatedAt.Add(time.Nanosecond)
	if err := ApplyInstancePlan(px, e2.sess.stores(e2.store, &j)); !errors.Is(err, ErrPlanStale) {
		t.Fatalf("want ErrPlanStale, got %v", err)
	}
	if e2.sess.recs["x"].InstanceID != "" {
		t.Fatal("no id may be written")
	}
}

// P2-7: restoring a dropped id needs path/project agreement and a single
// bound claimant.
func TestRestoreRequiresConsistentSingleClaimant(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("s", "local", -time.Second)
	var j []JournalEntry
	if err := ApplyInstancePlan(e.plan(), e.sess.stores(e.store, &j)); err != nil {
		t.Fatal(err)
	}
	e.sess.recs["s"].InstanceID = ""
	e.sess.recs["s"].Path = "/wt/elsewhere"
	if it := e.plan().agent(a.ID); it.Reason != SkipPathMismatch {
		t.Fatalf("moved record: %+v", it)
	}
}

func TestPlanToleratesNilAgents(t *testing.T) {
	e := newInstEnv(t)
	e.legacy("s", "local", -time.Second)
	_ = PlanInstances(e.sess.clone(), append(e.list(), nil), func(n string) string { return session.NewInstanceID() })
}

// P2-2: an older DevX binary rewrites agent.json and drops the binding. The
// event log still records it; the plan restores THAT binding (not a fresh
// legacy bind), and only if the session is still the same instance.
func TestPlanRestoresBindingDroppedFromAgentRecord(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("s", "local", -time.Second)
	var j []JournalEntry
	if err := ApplyInstancePlan(e.plan(), e.sess.stores(e.store, &j)); err != nil {
		t.Fatal(err)
	}
	bound, _ := e.store.LoadAgent(a.ID)
	id, at := bound.SessionInstanceID, bound.SessionCreatedAt
	if rid, rat, err := e.store.RecordedBinding(a.ID); err != nil || rid != id || !rat.Equal(at) {
		t.Fatalf("event log binding: %q %v %v", rid, rat, err)
	}
	// Old writer drops the agent fields.
	bound.SessionInstanceID, bound.SessionCreatedAt = "", time.Time{}
	_ = e.store.WithAgentLock(a.ID, func() error { return e.store.SaveAgent(bound) })
	plan := func() *InstancePlan {
		return PlanInstancesWithHistory(e.sess.clone(), e.list(), func(n string) string { return session.NewInstanceID() }, e.store.RecordedBinding)
	}
	p := plan()
	if it := p.agent(a.ID); it.Action != ActionRestoreBinding || it.SessionInstanceID != id {
		t.Fatalf("restore: %+v", it)
	}
	if err := ApplyInstancePlan(p, e.sess.stores(e.store, &j)); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.store.LoadAgent(a.ID); got.SessionInstanceID != id || !got.SessionCreatedAt.Equal(at) {
		t.Fatalf("not restored: %+v", got)
	}
	// Dropped again, and meanwhile the session was recreated: never
	// restored onto the new instance (skip, fail closed).
	got, _ := e.store.LoadAgent(a.ID)
	got.SessionInstanceID, got.SessionCreatedAt = "", time.Time{}
	_ = e.store.WithAgentLock(a.ID, func() error { return e.store.SaveAgent(got) })
	e.sess.recs["s"] = &session.Session{Name: "s", ProjectAlias: "proj", Path: "/wt/s", CreatedAt: e.t0.Add(-time.Hour),
		InstanceID: session.NewInstanceID(), LocalOnly: &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: a.ID}}
	if it := plan().agent(a.ID); it.Action != ActionSkip || it.Reason != SkipBoundElsewhere {
		t.Fatalf("recreated: %+v", it)
	}
}

// L1: the event-log restore path applies the same conflict checks.
func TestRestoreFromEventLogRespectsConflicts(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("s", "local", -time.Second)
	var j []JournalEntry
	if err := ApplyInstancePlan(e.plan(), e.sess.stores(e.store, &j)); err != nil {
		t.Fatal(err)
	}
	got, _ := e.store.LoadAgent(a.ID)
	got.SessionInstanceID, got.SessionCreatedAt = "", time.Time{}
	_ = e.store.WithAgentLock(a.ID, func() error { return e.store.SaveAgent(got) })
	e.sess.recs["s"].LocalOnly.AgentID = "pa_ffffffffffff" // foreign marker
	p := PlanInstancesWithHistory(e.sess.clone(), e.list(), func(string) string { return session.NewInstanceID() }, e.store.RecordedBinding)
	if it := p.agent(a.ID); it.Action != ActionSkip {
		t.Fatalf("foreign marker must block restore: %+v", it)
	}
}
