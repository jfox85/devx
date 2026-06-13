package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// setupTempHome points HOME at a fresh temp dir so sessions.json is isolated.
func setupTempHome(t *testing.T) {
	t.Helper()
	tmpDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	t.Cleanup(func() { os.Setenv("HOME", oldHome) })
}

// TestUpdateSessionNoLostUpdate reproduces the sessions.json lost-update race:
// two independently-loaded stores (e.g. the gatepost create CLI and the TUI /
// web daemon) each mutate a different field. Because UpdateSession re-reads the
// latest store under the lock before mutating, neither change is clobbered.
//
// Before the fix (full-store Save of a stale snapshot), the second writer would
// overwrite the first writer's field — exactly how gatepost Target metadata was
// being wiped.
func TestUpdateSessionNoLostUpdate(t *testing.T) {
	setupTempHome(t)

	seed, err := LoadSessions()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := seed.AddSession("s1", "branch", "/path", nil); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Two stores loaded from the same on-disk state, BEFORE either mutation.
	storeA, err := LoadSessions()
	if err != nil {
		t.Fatalf("load A: %v", err)
	}
	storeB, err := LoadSessions()
	if err != nil {
		t.Fatalf("load B: %v", err)
	}

	// Writer A sets Target (mimics gatepost create writing Target last).
	if err := storeA.UpdateSession("s1", func(s *Session) {
		s.Target = TargetMeta{Type: "gatepost", Gatepost: GatepostMeta{Enabled: true, LogsURL: "http://127.0.0.1:9999"}}
	}); err != nil {
		t.Fatalf("update A: %v", err)
	}

	// Writer B (stale snapshot: never saw Target) sets an unrelated field.
	if err := storeB.UpdateSession("s1", func(s *Session) {
		s.AttentionFlag = true
		s.AttentionReason = "claude_done"
	}); err != nil {
		t.Fatalf("update B: %v", err)
	}

	// Re-read from disk: BOTH updates must be present.
	final, err := LoadSessions()
	if err != nil {
		t.Fatalf("load final: %v", err)
	}
	s1, ok := final.Sessions["s1"]
	if !ok {
		t.Fatal("s1 missing")
	}
	if s1.Target.Type != "gatepost" || !s1.Target.Gatepost.Enabled || s1.Target.Gatepost.LogsURL != "http://127.0.0.1:9999" {
		t.Errorf("Target lost-update: got %+v", s1.Target)
	}
	if !s1.AttentionFlag || s1.AttentionReason != "claude_done" {
		t.Errorf("AttentionFlag lost-update: flag=%v reason=%q", s1.AttentionFlag, s1.AttentionReason)
	}
}

// TestConcurrentUpdatesAllSurvive hammers UpdateSession from many goroutines,
// each setting a distinct route on the same session. Under the file lock +
// re-read pattern, every write must survive.
func TestConcurrentUpdatesAllSurvive(t *testing.T) {
	setupTempHome(t)

	seed, err := LoadSessions()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := seed.AddSession("s1", "branch", "/path", nil); err != nil {
		t.Fatalf("add: %v", err)
	}

	const n = 25
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each goroutine loads its own store (stale snapshots) and updates.
			st, err := LoadSessions()
			if err != nil {
				t.Errorf("load %d: %v", i, err)
				return
			}
			key := fmt.Sprintf("svc%d", i)
			val := fmt.Sprintf("host%d", i)
			if err := st.UpdateSession("s1", func(s *Session) {
				if s.Routes == nil {
					s.Routes = map[string]string{}
				}
				s.Routes[key] = val
			}); err != nil {
				t.Errorf("update %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	final, err := LoadSessions()
	if err != nil {
		t.Fatalf("load final: %v", err)
	}
	s1 := final.Sessions["s1"]
	if got := len(s1.Routes); got != n {
		t.Fatalf("expected %d routes to survive concurrent writers, got %d: %v", n, got, s1.Routes)
	}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("svc%d", i)
		if s1.Routes[key] != fmt.Sprintf("host%d", i) {
			t.Errorf("route %s missing/wrong: %q", key, s1.Routes[key])
		}
	}
}

// TestSaveIsAtomic verifies Save publishes a complete, valid file via the temp
// file + rename path and leaves no stray temp files behind. A regression to an
// in-place truncating write would risk a torn/partial file for the lock-free
// reader; here we at least assert the published file always parses and is
// complete after the write returns.
func TestSaveIsAtomic(t *testing.T) {
	setupTempHome(t)

	store, err := LoadSessions()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := store.AddSession("s1", "branch", "/path", map[string]int{"A": 1, "B": 2}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// The published file must be fully written and parseable (not partial).
	reloaded, err := LoadSessions()
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	s1, ok := reloaded.Sessions["s1"]
	if !ok {
		t.Fatal("s1 missing after save")
	}
	if s1.Branch != "branch" || s1.Path != "/path" || s1.Ports["A"] != 1 || s1.Ports["B"] != 2 {
		t.Errorf("published file is incomplete/corrupt: %+v", s1)
	}

	// No temp files should remain after a successful atomic publish.
	dir := filepath.Dir(getSessionsPath())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if len(name) >= 10 && name[:10] == ".sessions-" {
			t.Errorf("leftover temp file after atomic save: %s", name)
		}
	}
}
