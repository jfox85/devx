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
	// Verify is read-only and refuses once the marker is gone.
	if _, err := VerifyManagedAgent("legacy", "pa_111111111111"); err != nil {
		t.Fatal(err)
	}
	_ = ReleaseManagedAgent("legacy", "pa_111111111111")
	if _, err := VerifyManagedAgent("legacy", "pa_111111111111"); err == nil {
		t.Fatal("verify must refuse without the marker")
	}
	if got, _ := LoadSessions(); got.Sessions["legacy"].ManagedAgent != "" {
		t.Fatal("verify must not write")
	}
}

// Derived migration ids are deterministic for the same record state and key,
// differ for a recreated record (new created_at) and for another key.
func TestDeriveInstanceID(t *testing.T) {
	k := []byte("0123456789abcdef0123456789abcdef")
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	a := DeriveInstanceID(k, "s", "/w/s", t0)
	if !ValidInstanceID(a) || a != DeriveInstanceID(k, "s", "/w/s", t0.In(time.FixedZone("X", 3600))) {
		t.Fatal("must be valid and zone-independent")
	}
	if a == DeriveInstanceID(k, "s", "/w/s", t0.Add(time.Nanosecond)) || a == DeriveInstanceID([]byte("other-key-other-key-other-key-xx"), "s", "/w/s", t0) {
		t.Fatal("must change with created_at and key")
	}
}
