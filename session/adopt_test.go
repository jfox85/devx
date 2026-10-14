package session

import (
	"errors"
	"testing"
	"time"
)

func TestAdoptManagedAgentRules(t *testing.T) {
	setupTempHome(t)
	now := time.Now()
	err := (&SessionStore{}).Mutate(func(s *SessionStore) error {
		s.Sessions["human"] = &Session{Name: "human", Path: "/w/human", ProjectAlias: "devx", Ports: map[string]int{"WEB": 52856}, CreatedAt: now}
		s.Sessions["local"] = &Session{Name: "local", Path: "/w/local", LocalOnly: &LocalOnlyMeta{Owner: LocalOnlyOwnerPiMCP, AgentID: "pa_aaaaaaaaaaaa"}, CreatedAt: now}
		s.Sessions["boxed"] = &Session{Name: "boxed", Path: "/w/boxed", Target: TargetMeta{Type: "gatepost"}, CreatedAt: now}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"local", "boxed", "missing"} {
		if _, err := AdoptManagedAgent(name, "pa_111111111111"); !errors.Is(err, ErrAdoptNotAllowed) {
			t.Fatalf("%s: want ErrAdoptNotAllowed, got %v", name, err)
		}
	}
	s, err := AdoptManagedAgent("human", "pa_111111111111")
	if err != nil || s.ManagedAgent != "pa_111111111111" {
		t.Fatalf("adopt: %+v %v", s, err)
	}
	if _, err := AdoptManagedAgent("human", "pa_111111111111"); err != nil {
		t.Fatalf("same agent must be idempotent: %v", err)
	}
	if _, err := AdoptManagedAgent("human", "pa_222222222222"); !errors.Is(err, ErrAdoptNotAllowed) {
		t.Fatalf("second agent must be refused, got %v", err)
	}
	// Ports, routes and every other field are unchanged.
	st, _ := LoadSessions()
	got, _ := st.GetSession("human")
	if got.Ports["WEB"] != 52856 || got.IsLocalOnly() || got.TargetType() != "host" || got.Path != "/w/human" {
		t.Fatalf("session changed: %+v", got)
	}
	// Release only clears our own marker.
	_ = ReleaseManagedAgent("human", "pa_999999999999")
	if st, _ = LoadSessions(); st.Sessions["human"].ManagedAgent != "pa_111111111111" {
		t.Fatal("foreign release cleared the marker")
	}
	_ = ReleaseManagedAgent("human", "pa_111111111111")
	if st, _ = LoadSessions(); st.Sessions["human"].ManagedAgent != "" {
		t.Fatal("release did not clear the marker")
	}
}
