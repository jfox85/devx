package piagent

import (
	"testing"
	"time"

	"github.com/jfox85/devx/session"
)

// Rolling back an event_log restore must leave the agent restorable, exactly
// as before the apply: the logged binding stays, and a re-plan proposes the
// same restore again.
func TestRollbackOfEventLogRestoreKeepsAgentRestorable(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("s", "local", -time.Second)
	var j0 []JournalEntry
	if err := ApplyInstancePlan(e.plan(), e.sess.stores(e.store, &j0)); err != nil {
		t.Fatal(err)
	}
	bound, _ := e.store.LoadAgent(a.ID)
	id := bound.SessionInstanceID
	bound.SessionInstanceID, bound.SessionCreatedAt = "", time.Time{}
	_ = e.store.WithAgentLock(a.ID, func() error { return e.store.SaveAgent(bound) })
	e.sess.recs["s"].InstanceID = ""
	before, _, _ := e.store.RecordedBinding(a.ID)
	p := PlanInstancesWithHistory(e.sess.clone(), e.list(), func(string) string { return session.NewInstanceID() }, e.store.RecordedBinding)
	if p.agent(a.ID).Action != ActionRestoreBinding {
		t.Fatalf("restore plan: %+v", p.agent(a.ID))
	}
	var j []JournalEntry
	if err := ApplyInstancePlan(p, e.sess.stores(e.store, &j)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RollbackInstancePlan(p, j, e.sess.stores(e.store, &[]JournalEntry{})); err != nil {
		t.Fatal(err)
	}
	after, _, _ := e.store.RecordedBinding(a.ID)
	if before != id || after != before {
		t.Fatalf("rollback changed the logged binding: before=%q after=%q (want %q)", before, after, id)
	}
	// The undo is still recorded, with an event RecordedBinding ignores.
	evs, _, _ := e.store.ReadEvents(a.ID, 0, MaxEventsPage)
	undone := false
	for _, ev := range evs {
		undone = undone || ev.Type == "session_instance_restore_undone"
	}
	if !undone {
		t.Fatal("rollback of a restore must be recorded")
	}
	again := PlanInstancesWithHistory(e.sess.clone(), e.list(), func(string) string { return session.NewInstanceID() }, e.store.RecordedBinding)
	if it := again.agent(a.ID); it.Action != ActionRestoreBinding || it.SessionInstanceID != id {
		t.Fatalf("after rollback the agent is no longer restorable: %+v", it)
	}
}

// Rolling back B (an event_log restore) and then A (the bind it restored),
// newest first as documented, must leave the agent as before A: no binding
// in agent.json and none in the log, so no plan can re-apply it without the
// owner.
func TestRollbackRestoreThenBindLeavesNoRecordedBinding(t *testing.T) {
	e := newInstEnv(t)
	a := e.legacy("s", "local", -time.Second)
	pa := e.plan()
	var ja []JournalEntry
	if err := ApplyInstancePlan(pa, e.sess.stores(e.store, &ja)); err != nil {
		t.Fatal(err)
	}
	bound, _ := e.store.LoadAgent(a.ID)
	bound.SessionInstanceID, bound.SessionCreatedAt = "", time.Time{}
	_ = e.store.WithAgentLock(a.ID, func() error { return e.store.SaveAgent(bound) })
	e.sess.recs["s"].InstanceID = ""
	pb := PlanInstancesWithHistory(e.sess.clone(), e.list(), func(string) string { return session.NewInstanceID() }, e.store.RecordedBinding)
	if pb.agent(a.ID).Action != ActionRestoreBinding {
		t.Fatalf("restore plan: %+v", pb.agent(a.ID))
	}
	var jb []JournalEntry
	if err := ApplyInstancePlan(pb, e.sess.stores(e.store, &jb)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RollbackInstancePlan(pb, jb, e.sess.stores(e.store, &[]JournalEntry{})); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RollbackInstancePlan(pa, ja, e.sess.stores(e.store, &[]JournalEntry{})); err != nil {
		t.Fatal(err)
	}
	if id, _, _ := e.store.RecordedBinding(a.ID); id != "" {
		t.Fatalf("after rolling back both runs the log still records binding %q", id)
	}
	if got, _ := e.store.LoadAgent(a.ID); got.SessionInstanceID != "" {
		t.Fatalf("agent still bound: %+v", got)
	}
	again := PlanInstancesWithHistory(e.sess.clone(), e.list(), func(string) string { return session.NewInstanceID() }, e.store.RecordedBinding)
	if it := again.agent(a.ID); it.Action == ActionRestoreBinding {
		t.Fatalf("a binding the owner rolled back would be re-applied: %+v", it)
	}
}
