package piagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jfox85/devx/session"
)

// All fixtures here are synthetic: in-memory session records, a temp agent
// store, fake tmux tags and fake backups. Nothing reads real DevX state.

type markerEnv struct {
	t       *testing.T
	store   *Store
	sess    *memSessions
	t0      time.Time
	backups []MarkerBackup
	tags    map[string]string // tmux session -> tag ("" = present but untagged)
	tagsMu  sync.Mutex
	confirm []string
	journal []MarkerJournalEntry
	jmu     sync.Mutex
	// backupSum overrides the hash VerifyBackup reports (tamper).
	backupSum map[string]string
	n         int
}

func newMarkerEnv(t *testing.T) *markerEnv {
	return &markerEnv{t: t, store: NewStore(filepath.Join(t.TempDir(), "state")),
		sess: &memSessions{recs: map[string]*session.Session{}},
		t0:   time.Date(2026, 10, 8, 22, 32, 42, 117391000, time.FixedZone("PDT", -7*3600)),
		tags: map[string]string{}, backupSum: map[string]string{}}
}

// lost adds an MCP-created agent whose session lost its marker, with all
// witnesses present: creation event, tmux tag and one backup holding the
// marker. Returns the agent.
func (e *markerEnv) lost(name string) *Agent {
	e.t.Helper()
	e.n++
	id := fmt.Sprintf("pa_%012x", e.n)
	wt := wtPath(name)
	sc := e.t0.Add(time.Duration(e.n) * time.Hour)
	e.sess.recs[name] = &session.Session{Name: name, ProjectAlias: "proj", Path: wt, CreatedAt: sc}
	a := &Agent{ID: id, DevxSession: name, Project: "proj", Worktree: wt, PiSessionID: "x",
		CreatedAt: sc.Add(59 * time.Millisecond).UTC(), CreatedBy: "mcp"}
	e.saveAgent(a)
	if err := e.store.WithAgentLock(id, func() error {
		_, err := e.store.AppendEvent(id, Event{Type: "agent_created", Source: "devx", Data: map[string]any{"session": name, "pi_session_id": "x"}})
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	e.tags[name] = id
	e.addBackupRecord("b1", name, func(s *session.Session) {
		s.LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: id, CreatedAt: sc}
	})
	return a
}

func (e *markerEnv) saveAgent(a *Agent) {
	e.t.Helper()
	if err := e.store.ensureAgentDirs(a.ID); err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.WithAgentLock(a.ID, func() error { return e.store.SaveAgent(a) }); err != nil {
		e.t.Fatal(err)
	}
}

// addBackupRecord copies the CURRENT record of name into backup bname and
// lets mod change it.
func (e *markerEnv) addBackupRecord(bname, name string, mod func(*session.Session)) {
	var b *MarkerBackup
	for i := range e.backups {
		if e.backups[i].Path == "/backups/"+bname {
			b = &e.backups[i]
		}
	}
	if b == nil {
		e.backups = append(e.backups, MarkerBackup{Path: "/backups/" + bname, SHA256: "sum-" + bname, Sessions: map[string]*session.Session{}})
		b = &e.backups[len(e.backups)-1]
	}
	cp := *e.sess.recs[name]
	if mod != nil {
		mod(&cp)
	}
	b.Sessions[name] = &cp
}

func (e *markerEnv) witnesses() MarkerWitnesses {
	return MarkerWitnesses{Creation: e.store.CreationEventOf, TmuxTag: e.tmuxTag}
}

func (e *markerEnv) tmuxTag(name string) (string, bool, error) {
	e.tagsMu.Lock()
	defer e.tagsMu.Unlock()
	v, ok := e.tags[name]
	return v, ok, nil
}

func (e *markerEnv) plan() *MarkerPlan {
	e.t.Helper()
	agents, err := e.store.ListAgents()
	if err != nil {
		e.t.Fatal(err)
	}
	return PlanMarkerRepair(e.sess.clone(), agents, MarkerPlanOptions{Backups: e.backups, Witnesses: e.witnesses(), Confirmed: e.confirm})
}

func (e *markerEnv) stores() MarkerStores {
	var unused []JournalEntry
	base := e.sess.stores(e.store, &unused)
	return MarkerStores{
		MutateSessions: base.MutateSessions,
		Agents:         e.store,
		VerifyBackup: func(p string) (string, error) {
			if s, ok := e.backupSum[p]; ok {
				return s, nil
			}
			for _, b := range e.backups {
				if b.Path == p {
					return b.SHA256, nil
				}
			}
			return "", errors.New("missing")
		},
		TmuxTag: e.tmuxTag,
		Journal: func(j MarkerJournalEntry) error {
			e.jmu.Lock()
			defer e.jmu.Unlock()
			e.journal = append(e.journal, j)
			return nil
		},
	}
}

func (p *MarkerPlan) item(id string) MarkerPlanItem {
	for _, it := range p.Items {
		if it.AgentID == id {
			return it
		}
	}
	return MarkerPlanItem{}
}

func TestMarkerPlanCandidateNeedsConfirmationThenRestores(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	p := e.plan()
	it := p.item(a.ID)
	if it.Action != MarkerActionCandidate || p.HasWork() {
		t.Fatalf("unconfirmed agent must be a candidate only: %+v", it)
	}
	if it.TmuxTag != a.ID || len(it.Witnesses) != 1 || it.CreationSeq != 1 || it.Marker == nil {
		t.Fatalf("evidence missing: %+v", it)
	}
	e.confirm = []string{a.ID}
	p2 := e.plan()
	if p2.Hash == p.Hash || p2.item(a.ID).Action != MarkerActionRestore {
		t.Fatalf("confirmation must change the plan: %+v", p2.item(a.ID))
	}
	if err := ApplyMarkerPlan(p2, e.stores()); err != nil {
		t.Fatal(err)
	}
	got := e.sess.recs["core"].LocalOnly
	want := e.backups[0].Sessions["core"].LocalOnly
	if !sameMarker(got, want) {
		t.Fatalf("restored %+v want %+v", got, want)
	}
	if len(e.journal) != 1 || e.journal[0].PlanHash != p2.Hash {
		t.Fatalf("journal: %+v", e.journal)
	}
	// Only local_only changed: the record is otherwise identical.
	cur := *e.sess.recs["core"]
	cur.LocalOnly = nil
	bk := *e.backups[0].Sessions["core"]
	bk.LocalOnly = nil
	if fmt.Sprintf("%+v", cur) != fmt.Sprintf("%+v", bk) {
		t.Fatalf("other fields changed:\n%+v\n%+v", cur, bk)
	}
	// Re-plan: marker present, nothing to do; re-apply of the old plan is a no-op.
	if p3 := e.plan(); p3.item(a.ID).Action != MarkerActionPresent || p3.HasWork() {
		t.Fatalf("after apply: %+v", p3.item(a.ID))
	}
	if err := ApplyMarkerPlan(p2, e.stores()); err != nil {
		t.Fatalf("idempotent re-apply: %v", err)
	}
	if len(e.journal) != 1 {
		t.Fatal("re-apply must not journal a second write")
	}
}

func TestMarkerPlanRefusesAmbiguousProvenance(t *testing.T) {
	cases := map[string]struct {
		mod  func(e *markerEnv, a *Agent)
		want string
	}{
		"not mcp":   {func(e *markerEnv, a *Agent) { a.CreatedBy = "cli"; e.saveAgent(a) }, ""},
		"adopted":   {func(e *markerEnv, a *Agent) { a.Adopted = true; e.saveAgent(a) }, MarkerSkipAdopted},
		"retired":   {func(e *markerEnv, a *Agent) { now := time.Now(); a.RetiredAt = &now; e.saveAgent(a) }, MarkerSkipRetired},
		"no record": {func(e *markerEnv, a *Agent) { delete(e.sess.recs, a.DevxSession) }, MarkerSkipNoSession},
		"foreign marker": {func(e *markerEnv, a *Agent) {
			e.sess.recs[a.DevxSession].LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: "pa_999999999999"}
		}, MarkerSkipOtherMarker},
		"adoption marker": {func(e *markerEnv, a *Agent) { e.sess.recs[a.DevxSession].ManagedAgent = a.ID }, MarkerSkipOtherMarker},
		"path":            {func(e *markerEnv, a *Agent) { a.Worktree = "/elsewhere"; e.saveAgent(a) }, MarkerSkipPath},
		"project":         {func(e *markerEnv, a *Agent) { e.sess.recs[a.DevxSession].ProjectAlias = "other" }, MarkerSkipProject},
		"container":       {func(e *markerEnv, a *Agent) { e.sess.recs[a.DevxSession].Target.Type = "docker" }, MarkerSkipContainer},
		"has ports":       {func(e *markerEnv, a *Agent) { e.sess.recs[a.DevxSession].Ports = map[string]int{"WEB": 3000} }, MarkerSkipExposed},
		"has routes": {func(e *markerEnv, a *Agent) {
			e.sess.recs[a.DevxSession].Routes = map[string]string{"web": "x.localhost"}
		}, MarkerSkipExposed},
		"recreated (newer)": {func(e *markerEnv, a *Agent) {
			e.sess.recs[a.DevxSession].CreatedAt = a.CreatedAt.Add(time.Second)
		}, MarkerSkipOrdering},
		"gap too large": {func(e *markerEnv, a *Agent) {
			e.sess.recs[a.DevxSession].CreatedAt = a.CreatedAt.Add(-time.Hour)
		}, MarkerSkipGap},
		"second claimant": {func(e *markerEnv, a *Agent) {
			b := *a
			b.ID = "pa_aaaaaaaaaaaa"
			b.Worktree = "/other"
			e.saveAgent(&b)
		}, MarkerSkipClaimants},
		"shared worktree": {func(e *markerEnv, a *Agent) {
			e.sess.recs["twin"] = &session.Session{Name: "twin", Path: a.Worktree, ProjectAlias: "proj", CreatedAt: e.t0}
		}, MarkerSkipSharedWorktree},
		"bound elsewhere": {func(e *markerEnv, a *Agent) {
			a.SessionInstanceID, a.SessionCreatedAt = "si_aaaaaaaaaaaaaaaaaaaaaaaa", e.t0
			e.saveAgent(a)
		}, MarkerSkipInstance},
		"foreign instance id": {func(e *markerEnv, a *Agent) {
			e.sess.recs[a.DevxSession].InstanceID = "si_bbbbbbbbbbbbbbbbbbbbbbbb"
		}, MarkerSkipForeignID},
		"no creation event": {func(e *markerEnv, a *Agent) {
			_ = e.store.WithAgentLock(a.ID, func() error { return writeFileAtomic(e.store.eventsPath(a.ID), nil) })
		}, MarkerSkipNoEvent},
		"creation event names other session": {func(e *markerEnv, a *Agent) {
			_ = e.store.WithAgentLock(a.ID, func() error {
				_ = writeFileAtomic(e.store.eventsPath(a.ID), nil)
				_ = writeFileAtomic(e.store.seqPath(a.ID), []byte("0"))
				_, err := e.store.AppendEvent(a.ID, Event{Type: "agent_created", Source: "devx", Data: map[string]any{"session": "other"}})
				return err
			})
		}, MarkerSkipNoEvent},
		"no tmux session":  {func(e *markerEnv, a *Agent) { delete(e.tags, a.DevxSession) }, MarkerSkipTmuxMissing},
		"untagged tmux":    {func(e *markerEnv, a *Agent) { e.tags[a.DevxSession] = "" }, MarkerSkipTmuxMismatch},
		"foreign tmux tag": {func(e *markerEnv, a *Agent) { e.tags[a.DevxSession] = "pa_999999999999" }, MarkerSkipTmuxMismatch},
		"no backup holds marker": {func(e *markerEnv, a *Agent) {
			e.backups[0].Sessions[a.DevxSession].LocalOnly = nil
		}, MarkerSkipNoBackupWitness},
		"backup of a different record": {func(e *markerEnv, a *Agent) {
			e.backups[0].Sessions[a.DevxSession].CreatedAt = e.t0.Add(-48 * time.Hour)
		}, MarkerSkipNoBackupWitness},
		"backup marker names another agent": {func(e *markerEnv, a *Agent) {
			e.backups[0].Sessions[a.DevxSession].LocalOnly.AgentID = "pa_999999999999"
		}, MarkerSkipBackupConflict},
		"backup marker wrong time": {func(e *markerEnv, a *Agent) {
			e.backups[0].Sessions[a.DevxSession].LocalOnly.CreatedAt = e.t0.Add(-time.Minute)
		}, MarkerSkipBackupConflict},
		"backup has adoption marker too": {func(e *markerEnv, a *Agent) {
			e.backups[0].Sessions[a.DevxSession].ManagedAgent = a.ID
		}, MarkerSkipBackupConflict},
		"backups disagree": {func(e *markerEnv, a *Agent) {
			e.addBackupRecord("b2", a.DevxSession, func(s *session.Session) {
				s.LocalOnly = &session.LocalOnlyMeta{Owner: "someone-else", AgentID: a.ID, CreatedAt: s.CreatedAt}
			})
		}, MarkerSkipBackupConflict},
		"backup instance id differs": {func(e *markerEnv, a *Agent) {
			e.sess.recs[a.DevxSession].InstanceID = session.DeriveInstanceID(a.DevxSession, a.Worktree, e.sess.recs[a.DevxSession].CreatedAt)
			e.backups[0].Sessions[a.DevxSession].InstanceID = "si_cccccccccccccccccccccccc"
		}, MarkerSkipBackupInstanceID},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newMarkerEnv(t)
			a := e.lost("core")
			e.confirm = []string{a.ID}
			c.mod(e, a)
			p := e.plan()
			it := p.item(a.ID)
			if c.want == "" {
				if it.AgentID != "" {
					t.Fatalf("non-MCP agent must not be listed: %+v", it)
				}
			} else if it.Action != MarkerActionSkip || it.Reason != c.want {
				t.Fatalf("want skip %q, got %s %q", c.want, it.Action, it.Reason)
			}
			if p.HasWork() || it.Marker != nil {
				t.Fatal("ambiguous case must not plan a write")
			}
			if len(p.UnknownConfirmations) != 1 {
				t.Fatal("confirming a non-candidate must make the plan non-applicable")
			}
			if err := ApplyMarkerPlan(p, e.stores()); !errors.Is(err, ErrPlanStale) {
				t.Fatalf("apply of a plan with unknown confirmations must refuse: %v", err)
			}
		})
	}
}

// A migrated (derived-id) or bound record still qualifies; a second backup
// taken after the loss (no marker) is simply not a witness.
func TestMarkerPlanAcceptsMigratedAndBoundRecords(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	s := e.sess.recs["core"]
	s.InstanceID = session.DeriveInstanceID("core", s.Path, s.CreatedAt)
	a.SessionInstanceID, a.SessionCreatedAt = s.InstanceID, s.CreatedAt
	e.saveAgent(a)
	e.addBackupRecord("b0-after-loss", "core", func(bs *session.Session) { bs.InstanceID = "" })
	e.confirm = []string{a.ID}
	it := e.plan().item(a.ID)
	if it.Action != MarkerActionRestore || len(it.Witnesses) != 1 || it.Witnesses[0] != "/backups/b1" {
		t.Fatalf("got %+v", it)
	}
}

// Apply re-validates every condition: stale identity, tag changes, backup
// tampering and new conflicting markers all refuse without writing.
func TestMarkerApplyRefusesStaleState(t *testing.T) {
	cases := map[string]func(e *markerEnv, a *Agent){
		"session recreated": func(e *markerEnv, a *Agent) {
			e.sess.recs["core"].CreatedAt = e.sess.recs["core"].CreatedAt.Add(time.Nanosecond)
		},
		"instance id assigned since": func(e *markerEnv, a *Agent) {
			e.sess.recs["core"].InstanceID = "si_dddddddddddddddddddddddd"
		},
		"agent recreated": func(e *markerEnv, a *Agent) { a.CreatedAt = a.CreatedAt.Add(time.Second); e.saveAgent(a) },
		"agent retired":   func(e *markerEnv, a *Agent) { now := time.Now(); a.RetiredAt = &now; e.saveAgent(a) },
		"agent bound since": func(e *markerEnv, a *Agent) {
			a.SessionInstanceID = "si_eeeeeeeeeeeeeeeeeeeeeeee"
			e.saveAgent(a)
		},
		"tmux tag gone":    func(e *markerEnv, a *Agent) { delete(e.tags, "core") },
		"tmux tag foreign": func(e *markerEnv, a *Agent) { e.tags["core"] = "pa_999999999999" },
		"backup changed":   func(e *markerEnv, a *Agent) { e.backupSum["/backups/b1"] = "tampered" },
		"foreign marker appeared": func(e *markerEnv, a *Agent) {
			e.sess.recs["core"].LocalOnly = &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: "pa_999999999999"}
		},
		"adoption marker appeared": func(e *markerEnv, a *Agent) { e.sess.recs["core"].ManagedAgent = a.ID },
		"ports added since":        func(e *markerEnv, a *Agent) { e.sess.recs["core"].Ports = map[string]int{"WEB": 1} },
		"agent marker on another record": func(e *markerEnv, a *Agent) {
			e.sess.recs["other"] = &session.Session{Name: "other", Path: "/o", LocalOnly: &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: a.ID}}
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			e := newMarkerEnv(t)
			a := e.lost("core")
			e.confirm = []string{a.ID}
			p := e.plan()
			if !p.HasWork() {
				t.Fatal("setup: expected work")
			}
			mod(e, a)
			before := fmt.Sprintf("%+v", e.sess.recs["core"].LocalOnly)
			err := ApplyMarkerPlan(p, e.stores())
			if !errors.Is(err, ErrPlanStale) {
				t.Fatalf("want ErrPlanStale, got %v", err)
			}
			if fmt.Sprintf("%+v", e.sess.recs["core"].LocalOnly) != before || len(e.journal) != 0 {
				t.Fatal("refused apply must not write or journal")
			}
		})
	}
}

func TestMarkerApplyRejectsTamperedPlan(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	e.confirm = []string{a.ID}
	p := e.plan()
	p.Items[0].Marker.AgentID = "pa_999999999999"
	if err := ApplyMarkerPlan(p, e.stores()); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("tampered plan must be refused: %v", err)
	}
	// A re-hashed plan with a mismatched marker is still refused.
	p.Hash = p.computeHash()
	if err := ApplyMarkerPlan(p, e.stores()); !errors.Is(err, ErrPlanStale) {
		t.Fatalf("marker for another agent must be refused: %v", err)
	}
	if e.sess.recs["core"].LocalOnly != nil {
		t.Fatal("no write expected")
	}
}

// Interrupted apply: the sessions write fails after the journal entry
// (crash). Rollback of that journal is a no-op; re-applying completes.
func TestMarkerApplyInterruptedThenRetried(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	b := e.lost("side")
	e.confirm = []string{a.ID, b.ID}
	p := e.plan()
	e.sess.failAt = 2 // second sessions write "crashes"
	if err := ApplyMarkerPlan(p, e.stores()); err == nil {
		t.Fatal("expected simulated crash")
	}
	if e.sess.recs["core"].LocalOnly == nil || e.sess.recs["side"].LocalOnly != nil {
		t.Fatal("first write should have landed, second not")
	}
	if len(e.journal) != 2 {
		t.Fatalf("journal precedes each write: %d", len(e.journal))
	}
	// Rolling back the torn run removes the landed marker and skips the other.
	n, skipped, err := RollbackMarkers(e.journal, e.stores())
	if err != nil || n != 1 || len(skipped) != 1 {
		t.Fatalf("rollback n=%d skipped=%v err=%v", n, skipped, err)
	}
	// Re-plan and apply completes both.
	e.sess.failAt = 0
	e.journal = nil
	p2 := e.plan()
	if p2.Hash != p.Hash {
		t.Fatal("same records must give the same plan after rollback")
	}
	if err := ApplyMarkerPlan(p2, e.stores()); err != nil {
		t.Fatal(err)
	}
	if e.sess.recs["core"].LocalOnly == nil || e.sess.recs["side"].LocalOnly == nil {
		t.Fatal("retry must complete")
	}
	// Retry after partial success (no rollback) is also idempotent.
	if err := ApplyMarkerPlan(p2, e.stores()); err != nil {
		t.Fatal(err)
	}
}

// Rollback removes only the exact marker it wrote, from the exact record.
func TestMarkerRollbackOnlyRemovesOwnWrite(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	b := e.lost("side")
	c := e.lost("gone")
	e.confirm = []string{a.ID, b.ID, c.ID}
	if err := ApplyMarkerPlan(e.plan(), e.stores()); err != nil {
		t.Fatal(err)
	}
	// side: marker changed by someone else; gone: record removed.
	e.sess.recs["side"].LocalOnly.CreatedAt = time.Now()
	delete(e.sess.recs, "gone")
	n, skipped, err := RollbackMarkers(e.journal, e.stores())
	if err != nil || n != 1 || len(skipped) != 2 {
		t.Fatalf("n=%d skipped=%v err=%v", n, skipped, err)
	}
	if e.sess.recs["core"].LocalOnly != nil || e.sess.recs["side"].LocalOnly == nil {
		t.Fatal("rollback touched the wrong record")
	}
	// Second rollback is a no-op.
	if n, _, _ := RollbackMarkers(e.journal, e.stores()); n != 0 {
		t.Fatal("rollback must be idempotent")
	}
	evs, _, _ := e.store.ReadEvents(a.ID, 0, 100)
	var types []string
	for _, ev := range evs {
		types = append(types, ev.Type)
	}
	if strings.Join(types, ",") != "agent_created,local_only_marker_restored,local_only_marker_rollback" {
		t.Fatalf("events: %v", types)
	}
}

// Concurrent applies of the same plan write exactly once.
func TestMarkerConcurrentApplyWritesOnce(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	e.confirm = []string{a.ID}
	p := e.plan()
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = ApplyMarkerPlan(p, e.stores())
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(e.journal) != 1 || e.sess.recs["core"].LocalOnly == nil {
		t.Fatalf("want exactly one write, journal=%d", len(e.journal))
	}
}

// Older writer after repair: the marker is lost again (unmodified older
// binaries cannot preserve it), and the same witnesses re-plan the same
// repair; nothing is "magically" preserved.
func TestMarkerLostAgainByOlderWriterReplansSameRepair(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	e.confirm = []string{a.ID}
	p := e.plan()
	if err := ApplyMarkerPlan(p, e.stores()); err != nil {
		t.Fatal(err)
	}
	e.sess.recs["core"].LocalOnly = nil // older-schema rewrite
	if p2 := e.plan(); p2.Hash != p.Hash {
		t.Fatal("same evidence must yield the same plan")
	}
}

// Re-planning with the same confirmations after a partial apply counts the
// already-restored agent as satisfied (not an unknown confirmation), so the
// same command flow completes; the plan only restores what remains.
func TestMarkerReplanAfterPartialApplyKeepsConfirmationsValid(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	b := e.lost("side")
	e.confirm = []string{a.ID, b.ID}
	p := e.plan()
	e.sess.failAt = 2
	_ = ApplyMarkerPlan(p, e.stores())
	e.sess.failAt = 0
	p2 := e.plan()
	if len(p2.UnknownConfirmations) != 0 || len(p2.OwnerConfirmed) != 2 {
		t.Fatalf("confirmations after partial apply: unknown=%v confirmed=%v", p2.UnknownConfirmations, p2.OwnerConfirmed)
	}
	if p2.item(a.ID).Action != MarkerActionPresent || p2.item(b.ID).Action != MarkerActionRestore {
		t.Fatalf("got %s / %s", p2.item(a.ID).Action, p2.item(b.ID).Action)
	}
	if err := ApplyMarkerPlan(p2, e.stores()); err != nil {
		t.Fatal(err)
	}
	if e.sess.recs["side"].LocalOnly == nil {
		t.Fatal("remaining marker not restored")
	}
}

// Backups that disagree only in a marker member this build does not know
// are a conflict, not "first backup wins".
func TestMarkerBackupsDifferingInUnknownMemberConflict(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	e.addBackupRecord("b2", "core", func(s *session.Session) {
		m := *e.backups[0].Sessions["core"].LocalOnly
		m.Unknown = map[string]json.RawMessage{"session_instance_id": json.RawMessage(`"si_x"`)}
		s.LocalOnly = &m
	})
	e.confirm = []string{a.ID}
	if it := e.plan().item(a.ID); it.Action != MarkerActionSkip || it.Reason != MarkerSkipBackupConflict {
		t.Fatalf("got %s %q", it.Action, it.Reason)
	}
}

// Rollback acts only on journal entries that match a confirmed restoration
// of the run's plan.
func TestJournalMatchesPlan(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	e.confirm = []string{a.ID}
	p := e.plan()
	if err := ApplyMarkerPlan(p, e.stores()); err != nil {
		t.Fatal(err)
	}
	good := e.journal[0]
	if !JournalMatchesPlan(good, p) {
		t.Fatal("genuine entry rejected")
	}
	for name, mod := range map[string]func(*MarkerJournalEntry){
		"hash":    func(j *MarkerJournalEntry) { j.PlanHash = "x" },
		"session": func(j *MarkerJournalEntry) { j.Session = "other" },
		"agent":   func(j *MarkerJournalEntry) { j.AgentID = "pa_999999999999" },
		"created": func(j *MarkerJournalEntry) { j.SessionCreatedAt = j.SessionCreatedAt.Add(1) },
		"path":    func(j *MarkerJournalEntry) { j.SessionPath = "/x" },
		"marker":  func(j *MarkerJournalEntry) { j.Marker.CreatedAt = j.Marker.CreatedAt.Add(1) },
	} {
		j := good
		mod(&j)
		if JournalMatchesPlan(j, p) {
			t.Fatalf("%s: forged entry accepted", name)
		}
	}
}

// Rollback refuses a record whose path or instance id changed even though
// created_at and marker match.
func TestMarkerRollbackChecksPathAndInstance(t *testing.T) {
	for name, mod := range map[string]func(s *session.Session){
		"path":     func(s *session.Session) { s.Path = "/moved" },
		"instance": func(s *session.Session) { s.InstanceID = "si_ffffffffffffffffffffffff" },
	} {
		t.Run(name, func(t *testing.T) {
			e := newMarkerEnv(t)
			a := e.lost("core")
			e.sess.recs["core"].InstanceID = session.DeriveInstanceID("core", e.sess.recs["core"].Path, e.sess.recs["core"].CreatedAt)
			e.confirm = []string{a.ID}
			if err := ApplyMarkerPlan(e.plan(), e.stores()); err != nil {
				t.Fatal(err)
			}
			mod(e.sess.recs["core"])
			n, skipped, err := RollbackMarkers(e.journal, e.stores())
			if err != nil || n != 0 || len(skipped) != 1 || e.sess.recs["core"].LocalOnly == nil {
				t.Fatalf("n=%d skipped=%v err=%v", n, skipped, err)
			}
		})
	}
}

// Markers that differ only in a member this build does not know are not
// the same marker (apply refuses; rollback leaves it).
func TestMarkerComparisonIncludesUnknownMembers(t *testing.T) {
	a := &session.LocalOnlyMeta{Owner: "o", AgentID: "pa_1"}
	b := &session.LocalOnlyMeta{Owner: "o", AgentID: "pa_1", Unknown: map[string]json.RawMessage{"x": json.RawMessage(`1`)}}
	if sameMarker(a, b) {
		t.Fatal("unknown member difference ignored")
	}
	c := &session.LocalOnlyMeta{Owner: "o", AgentID: "pa_1", Unknown: map[string]json.RawMessage{"x": json.RawMessage(` 1 `)}}
	if !sameMarker(b, c) {
		t.Fatal("whitespace-only difference must compare equal")
	}
}

// A genuine journal entry still matches the plan after the plan is written
// to plan.json (MarshalIndent) and read back, including unknown marker
// members; a changed instance id does not match.
func TestJournalMatchesPlanAfterPlanFileRoundTrip(t *testing.T) {
	e := newMarkerEnv(t)
	a := e.lost("core")
	m := e.backups[0].Sessions["core"].LocalOnly
	m.Unknown = map[string]json.RawMessage{"future": json.RawMessage(`{"b":2,"a":"<x>"}`)}
	e.confirm = []string{a.ID}
	p := e.plan()
	if err := ApplyMarkerPlan(p, e.stores()); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	var back MarkerPlan
	if err := json.Unmarshal(data, &back); err != nil || !back.VerifyHash() {
		t.Fatalf("reloaded plan hash: %v", err)
	}
	jdata, _ := json.Marshal(e.journal[0])
	var j MarkerJournalEntry
	_ = json.Unmarshal(jdata, &j)
	if !JournalMatchesPlan(j, &back) {
		t.Fatal("genuine entry rejected after round trip")
	}
	j.SessionInstanceID = "si_ffffffffffffffffffffffff"
	if JournalMatchesPlan(j, &back) {
		t.Fatal("changed instance id accepted")
	}
}
