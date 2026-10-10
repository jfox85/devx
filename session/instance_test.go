package session

import (
	"testing"
	"time"
)

// Every DevX path that creates a session record assigns a fresh, valid
// instance id; ordinary saves preserve it; a removed and recreated session
// (same name and path) gets a different one; adoption gives a legacy record
// one without changing anything else.
func TestInstanceIDAssignedPreservedAndRenewed(t *testing.T) {
	setupTempHome(t)
	st := &SessionStore{}
	if err := st.AddSessionWithProject("a", "a", "/w/a", map[string]int{}, "p", "/p"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("b", "b", "/w/b", map[string]int{}); err != nil {
		t.Fatal(err)
	}
	fresh, _ := LoadSessions()
	a1, b1 := fresh.Sessions["a"].InstanceID, fresh.Sessions["b"].InstanceID
	if !ValidInstanceID(a1) || !ValidInstanceID(b1) || a1 == b1 {
		t.Fatalf("ids: %q %q", a1, b1)
	}
	// Ordinary updates (attach, flag, rename, Mutate) keep it.
	_ = fresh.RecordAttach("a")
	_ = SetAttentionFlag("a", "manual")
	_ = fresh.UpdateSession("a", func(s *Session) { s.DisplayName = "A" })
	if got, _ := LoadSessions(); got.Sessions["a"].InstanceID != a1 {
		t.Fatal("updates must preserve instance_id")
	}
	// Remove and recreate with the same name and path: new id.
	if err := fresh.RemoveSession("a"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSessionWithProject("a", "a", "/w/a", map[string]int{}, "p", "/p"); err != nil {
		t.Fatal(err)
	}
	if got, _ := LoadSessions(); got.Sessions["a"].InstanceID == a1 || !ValidInstanceID(got.Sessions["a"].InstanceID) {
		t.Fatal("recreated session must get a new instance id")
	}
	// Clear wipes the registry; anything recreated afterwards is new.
	if err := ClearRegistry(); err != nil {
		t.Fatal(err)
	}
	_ = st.AddSession("b", "b", "/w/b", map[string]int{})
	if got, _ := LoadSessions(); got.Sessions["b"].InstanceID == b1 {
		t.Fatal("session recreated after clear must get a new instance id")
	}
}

func TestAdoptAssignsInstanceToLegacyRecordOnly(t *testing.T) {
	setupTempHome(t)
	now := time.Now()
	_ = (&SessionStore{}).Mutate(func(s *SessionStore) error {
		s.Sessions["legacy"] = &Session{Name: "legacy", Path: "/w/legacy", Ports: map[string]int{"WEB": 1}, CreatedAt: now}
		s.Sessions["modern"] = &Session{Name: "modern", Path: "/w/modern", CreatedAt: now, InstanceID: "si_aaaaaaaaaaaaaaaaaaaaaaaa"}
		return nil
	})
	l, err := AdoptManagedAgent("legacy", "pa_111111111111")
	if err != nil || !ValidInstanceID(l.InstanceID) || l.Ports["WEB"] != 1 || !l.CreatedAt.Equal(now) {
		t.Fatalf("legacy adopt: %+v %v", l, err)
	}
	m, err := AdoptManagedAgent("modern", "pa_222222222222")
	if err != nil || m.InstanceID != "si_aaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("existing id must be kept: %+v %v", m, err)
	}
	// Verify is read-only. Unbound (legacy) agent: marker required.
	if _, err := VerifyManagedAgent("legacy", "pa_111111111111", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	inst := l.InstanceID
	_ = ReleaseManagedAgent("legacy", "pa_111111111111") // marker dropped
	if _, err := VerifyManagedAgent("legacy", "pa_111111111111", "", time.Time{}); err == nil {
		t.Fatal("unbound agent: verify must refuse without the marker")
	}
	// Bound agent: the instance proves identity even without the marker.
	if _, err := VerifyManagedAgent("legacy", "pa_111111111111", inst, now); err != nil {
		t.Fatalf("bound agent, marker dropped: %v", err)
	}
	// Old writer also dropped the id: exact created_at witness.
	_ = (&SessionStore{}).Mutate(func(s *SessionStore) error { s.Sessions["legacy"].InstanceID = ""; return nil })
	if _, err := VerifyManagedAgent("legacy", "pa_111111111111", inst, now); err != nil {
		t.Fatalf("id dropped, created_at matches: %v", err)
	}
	if _, err := VerifyManagedAgent("legacy", "pa_111111111111", inst, now.Add(time.Nanosecond)); err == nil {
		t.Fatal("created_at mismatch must refuse")
	}
	// Recreated (new id), even with the marker naming the agent: refuse.
	_ = (&SessionStore{}).Mutate(func(s *SessionStore) error {
		s.Sessions["legacy"] = &Session{Name: "legacy", Path: "/w/legacy", CreatedAt: now.Add(-time.Hour), InstanceID: NewInstanceID(), ManagedAgent: "pa_111111111111"}
		return nil
	})
	if _, err := VerifyManagedAgent("legacy", "pa_111111111111", inst, now); err == nil {
		t.Fatal("recreated session must not verify")
	}
	// Foreign marker: refuse even if the instance matched.
	_ = (&SessionStore{}).Mutate(func(s *SessionStore) error {
		s.Sessions["legacy"].InstanceID, s.Sessions["legacy"].ManagedAgent = inst, "pa_999999999999"
		return nil
	})
	if _, err := VerifyManagedAgent("legacy", "pa_111111111111", inst, now); err == nil {
		t.Fatal("foreign marker must refuse")
	}
	if got, _ := LoadSessions(); got.Sessions["legacy"].ManagedAgent != "pa_999999999999" {
		t.Fatal("verify must not write")
	}
}

// Derived migration ids are deterministic for the same record identity and
// differ for a recreated record (new created_at), another name or path.
func TestDeriveInstanceID(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	a := DeriveInstanceID("s", "/w/s", t0)
	if !ValidInstanceID(a) || a != DeriveInstanceID("s", "/w/s", t0.In(time.FixedZone("X", 3600))) {
		t.Fatal("must be valid and zone-independent")
	}
	for _, b := range []string{DeriveInstanceID("s", "/w/s", t0.Add(time.Nanosecond)), DeriveInstanceID("t", "/w/s", t0), DeriveInstanceID("s", "/w/t", t0)} {
		if a == b {
			t.Fatal("must change with created_at, name and path")
		}
	}
}
