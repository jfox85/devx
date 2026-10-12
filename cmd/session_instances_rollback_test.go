package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

// instRollbackFixture is a synthetic HOME with two pre-instance sessions
// marked by their agents, one applied migration run, and an unrelated
// victim session+agent the run never touched.
type instRollbackFixture struct {
	t    *testing.T
	home string
	cfg  string
	run  string // run directory --apply created
}

const (
	fxAgentA = "pa_00000000000a"
	fxAgentB = "pa_00000000000b"
	fxVictim = "pa_00000000000f"
	fxVicID  = "si_ffffffffffffffffffffffff"
)

func newInstRollbackFixture(t *testing.T) *instRollbackFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	resetInstanceFlags(t)
	f := &instRollbackFixture{t: t, home: home, cfg: filepath.Join(home, ".config", "devx")}
	if err := os.MkdirAll(f.cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	sessions := map[string]any{}
	for _, s := range []struct{ name, agent string }{{"a", fxAgentA}, {"b", fxAgentB}} {
		sessions[s.name] = map[string]any{"name": s.name, "project_alias": "synth", "branch": s.name, "path": f.wt(s.name),
			"ports": map[string]int{}, "created_at": t0, "updated_at": t0,
			"local_only": map[string]any{"owner": session.LocalOnlyOwnerPiMCP, "agent_id": s.agent, "created_at": t0}}
		f.writeAgent(s.agent, s.name, t0.Add(time.Second), "", time.Time{})
	}
	// The victim is already bound (not part of any plan).
	sessions["victim"] = map[string]any{"name": "victim", "project_alias": "synth", "branch": "victim", "path": f.wt("victim"),
		"ports": map[string]int{}, "created_at": t0, "updated_at": t0, "instance_id": fxVicID}
	f.writeAgent(fxVictim, "victim", t0.Add(time.Second), fxVicID, t0)
	b, _ := json.MarshalIndent(map[string]any{"sessions": sessions}, "", "  ")
	if err := os.WriteFile(filepath.Join(f.cfg, "sessions.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	f.run = f.apply()
	return f
}

func resetInstanceFlags(t *testing.T) {
	instancesApplyPlan, instancesRollback, instancesJSON, instancesForceOlder, instancesConfirm = "", "", false, false, nil
	t.Cleanup(func() {
		instancesApplyPlan, instancesRollback, instancesJSON, instancesForceOlder, instancesConfirm = "", "", false, false, nil
	})
}

func (f *instRollbackFixture) wt(name string) string { return filepath.Join(f.home, "wt", name) }

func (f *instRollbackFixture) agentPath(id string) string {
	return filepath.Join(f.cfg, "pi-agents", "agents", id, "agent.json")
}

func (f *instRollbackFixture) writeAgent(id, sess string, created time.Time, bound string, boundAt time.Time) {
	f.t.Helper()
	rec := map[string]any{"id": id, "devx_session": sess, "project": "synth", "worktree": f.wt(sess), "pi_session_id": "x",
		"binding": map[string]any{"tmux_session": sess}, "lease": map[string]any{"holder": "human", "generation": 1},
		"created_at": created, "updated_at": created}
	if bound != "" {
		rec["session_instance_id"], rec["session_created_at"] = bound, boundAt
	}
	_ = os.MkdirAll(filepath.Dir(f.agentPath(id)), 0o700)
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(f.agentPath(id), b, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func runInstancesCmd(args func()) (string, error) {
	var buf bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&buf)
	args()
	defer func() { instancesApplyPlan, instancesRollback = "", "" }()
	err := runSessionInstances(c, nil)
	return buf.String(), err
}

// apply reviews and applies the current plan through the real command path
// and returns the run directory it printed.
func (f *instRollbackFixture) apply() string {
	f.t.Helper()
	plan, err := currentInstancePlan()
	if err != nil || !plan.HasWork() {
		f.t.Fatalf("plan: %v %+v", err, plan)
	}
	out, err := runInstancesCmd(func() { instancesApplyPlan = plan.Hash })
	if err != nil {
		f.t.Fatalf("apply: %v\n%s", err, out)
	}
	m := regexp.MustCompile(`--rollback (\S+)`).FindStringSubmatch(out)
	if m == nil {
		f.t.Fatalf("no run dir in:\n%s", out)
	}
	return m[1]
}

func (f *instRollbackFixture) rollback(dir string) (string, error) {
	return runInstancesCmd(func() { instancesRollback = dir })
}

func (f *instRollbackFixture) sessions() map[string]*session.Session {
	f.t.Helper()
	st, err := session.LoadSessions()
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Sessions
}

func (f *instRollbackFixture) agent(id string) *piagent.Agent {
	f.t.Helper()
	a, err := piagent.NewStore(piAgentStateDir()).LoadAgent(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}

// snapshot is the exact bytes of every record rollback could touch.
func (f *instRollbackFixture) snapshot() string {
	f.t.Helper()
	var parts []string
	for _, p := range []string{filepath.Join(f.cfg, "sessions.json"), f.agentPath(fxAgentA), f.agentPath(fxAgentB), f.agentPath(fxVictim)} {
		b, err := os.ReadFile(p)
		if err != nil {
			f.t.Fatal(err)
		}
		parts = append(parts, string(b))
	}
	return strings.Join(parts, "\n--\n")
}

// refused asserts a rollback of dir fails mentioning want and writes nothing.
func (f *instRollbackFixture) refused(dir, want string) {
	f.t.Helper()
	before := f.snapshot()
	marker := filepath.Join(dir, "rolled-back")
	markerBefore, _ := os.ReadFile(marker)
	out, err := f.rollback(dir)
	if err == nil || !strings.Contains(err.Error(), want) {
		f.t.Fatalf("rollback of %s must be refused with %q, got err=%v out=%s", dir, want, err, out)
	}
	if f.snapshot() != before {
		f.t.Fatalf("refused rollback of %s changed records", dir)
	}
	if markerAfter, _ := os.ReadFile(marker); string(markerAfter) != string(markerBefore) {
		f.t.Fatalf("refused rollback of %s wrote a rolled-back marker", dir)
	}
}

// cloneRun copies the applied run into a sibling run directory with the
// given name (mode-preserving), for tampering tests.
func (f *instRollbackFixture) cloneRun(name string) string {
	f.t.Helper()
	dst := filepath.Join(filepath.Dir(f.run), name)
	if err := os.MkdirAll(dst, 0o700); err != nil {
		f.t.Fatal(err)
	}
	for _, n := range []string{"plan.json", "journal.jsonl"} {
		b, err := os.ReadFile(filepath.Join(f.run, n))
		if err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, n), b, 0o600); err != nil {
			f.t.Fatal(err)
		}
	}
	return dst
}

func (f *instRollbackFixture) runPlan() *piagent.InstancePlan {
	f.t.Helper()
	var p piagent.InstancePlan
	b, _ := os.ReadFile(filepath.Join(f.run, "plan.json"))
	if err := json.Unmarshal(b, &p); err != nil {
		f.t.Fatal(err)
	}
	return &p
}

func (f *instRollbackFixture) assertApplied() {
	f.t.Helper()
	s := f.sessions()
	for _, c := range []struct{ sess, agent string }{{"a", fxAgentA}, {"b", fxAgentB}} {
		if s[c.sess].InstanceID == "" || f.agent(c.agent).SessionInstanceID != s[c.sess].InstanceID {
			f.t.Fatalf("%s/%s not applied", c.sess, c.agent)
		}
	}
	f.assertVictimIntact()
}

func (f *instRollbackFixture) assertVictimIntact() {
	f.t.Helper()
	if f.sessions()["victim"].InstanceID != fxVicID || f.agent(fxVictim).SessionInstanceID != fxVicID {
		f.t.Fatal("victim record (never in any plan) was changed")
	}
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceRollbackRefusesForgedOrMisselectedDirectory(t *testing.T) {
	f := newInstRollbackFixture(t)
	forgedJournal := []string{
		`{"kind":"session","name":"victim","instance_id":"` + fxVicID + `"}`,
		`{"kind":"agent","name":"` + fxVictim + `","instance_id":"` + fxVicID + `"}`,
	}
	// Reproduction of PR83 r4243045158: any dir with a parseable journal.
	outside := filepath.Join(t.TempDir(), "forged")
	_ = os.MkdirAll(outside, 0o700)
	writeLines(t, filepath.Join(outside, "journal.jsonl"), forgedJournal...)
	f.refused(outside, "not a run directory directly under")
	// Even with the applied run's real plan copied next to it.
	b, _ := os.ReadFile(filepath.Join(f.run, "plan.json"))
	_ = os.WriteFile(filepath.Join(outside, "plan.json"), b, 0o600)
	f.refused(outside, "not a run directory directly under")
	// Traversal out of backups into a sibling run-named dir is refused...
	sibling := filepath.Join(f.cfg, filepath.Base(f.run))
	_ = os.MkdirAll(sibling, 0o700)
	for _, n := range []string{"plan.json", "journal.jsonl"} {
		b, _ := os.ReadFile(filepath.Join(f.run, n))
		_ = os.WriteFile(filepath.Join(sibling, n), b, 0o600)
	}
	f.refused(filepath.Dir(f.run)+"/../"+filepath.Base(f.run), "not a run directory directly under")
	// Wrong kind of directory under backups (e.g. a marker run, or any name).
	for _, name := range []string{"session-markers-20261001T000000Z-" + f.runPlan().Hash + "-x", "random", "session-instances-x", "session-instances-a-b-c-d"} {
		f.refused(f.cloneRun(name), "not a session-instances-* run directory")
	}
	// The backups dir itself and the sessions dir are not runs.
	f.refused(filepath.Dir(f.run), "not a run directory directly under")
	f.refused(f.cfg, "not a run directory directly under")
	f.assertApplied()
	// ...while an unclean path that resolves to the genuine run is
	// normalized and accepted.
	_ = os.RemoveAll(sibling)
	for _, e := range mustReadDir(t, filepath.Dir(f.run)) {
		if filepath.Join(filepath.Dir(f.run), e) != f.run {
			_ = os.RemoveAll(filepath.Join(filepath.Dir(f.run), e))
		}
	}
	unclean := filepath.Dir(f.run) + "/../backups/./" + filepath.Base(f.run)
	if out, err := f.rollback(unclean); err != nil || !strings.Contains(out, "Rolled back 4 change(s)") {
		t.Fatalf("unclean genuine path: %v\n%s", err, out)
	}
}

func TestInstanceRollbackRefusesUnsafeFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink/mode/owner checks are POSIX-only")
	}
	f := newInstRollbackFixture(t)
	hash := f.runPlan().Hash
	name := func(s string) string { return "session-instances-20261001T000000Z-" + hash + "-" + s }

	// Symlinked run directory pointing at a real run elsewhere.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	_ = os.MkdirAll(elsewhere, 0o700)
	for _, n := range []string{"plan.json", "journal.jsonl"} {
		b, _ := os.ReadFile(filepath.Join(f.run, n))
		_ = os.WriteFile(filepath.Join(elsewhere, n), b, 0o600)
	}
	link := filepath.Join(filepath.Dir(f.run), name("link"))
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	f.refused(link, "symlinks are refused")

	// Symlinked plan.json / journal.jsonl.
	for _, n := range []string{"plan.json", "journal.jsonl"} {
		d := f.cloneRun(name("sym" + strings.TrimSuffix(n, filepath.Ext(n))))
		_ = os.Remove(filepath.Join(d, n))
		if err := os.Symlink(filepath.Join(f.run, n), filepath.Join(d, n)); err != nil {
			t.Fatal(err)
		}
		f.refused(d, "symlinks are refused")
	}
	// Missing, non-regular, or group/other-writable files.
	d := f.cloneRun(name("nojournal"))
	_ = os.Remove(filepath.Join(d, "journal.jsonl"))
	f.refused(d, "journal in")
	d = f.cloneRun(name("dirjournal"))
	_ = os.Remove(filepath.Join(d, "journal.jsonl"))
	_ = os.Mkdir(filepath.Join(d, "journal.jsonl"), 0o700)
	f.refused(d, "not a regular file")
	for _, n := range []string{"plan.json", "journal.jsonl"} {
		d := f.cloneRun(name("gw" + strings.TrimSuffix(n, filepath.Ext(n))))
		if err := os.Chmod(filepath.Join(d, n), 0o620); err != nil {
			t.Fatal(err)
		}
		f.refused(d, "writable by group or others")
	}
	// Group/other-writable run directory.
	d = f.cloneRun(name("gwdir"))
	if err := os.Chmod(d, 0o777); err != nil {
		t.Fatal(err)
	}
	f.refused(d, "writable by group or others")

	// Symlinked backups directory: the base itself must be a real dir.
	base := filepath.Dir(f.run)
	real := base + ".real"
	if err := os.Rename(base, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, base); err != nil {
		t.Fatal(err)
	}
	f.refused(f.run, "backups directory")
	_ = os.Remove(base)
	_ = os.Rename(real, base)
	f.assertApplied()
	// The genuine run still rolls back afterwards (newest-run check
	// ignores the clones only once they are removed).
	for _, e := range mustReadDir(t, base) {
		if filepath.Join(base, e) != f.run {
			_ = os.RemoveAll(filepath.Join(base, e))
		}
	}
	if out, err := f.rollback(f.run); err != nil {
		t.Fatalf("genuine rollback: %v\n%s", err, out)
	}
}

func mustReadDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestInstanceRollbackRefusesTamperedPlanOrJournal(t *testing.T) {
	f := newInstRollbackFixture(t)
	plan := f.runPlan()
	runName := func(hash, s string) string { return "session-instances-20261001T000000Z-" + hash + "-" + s }
	journal, _ := os.ReadFile(filepath.Join(f.run, "journal.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(journal)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 2 session + 2 agent entries, got:\n%s", journal)
	}

	// Edited plan content (hash no longer matches).
	d := f.cloneRun(runName(plan.Hash, "edited"))
	pb, _ := os.ReadFile(filepath.Join(d, "plan.json"))
	_ = os.WriteFile(filepath.Join(d, "plan.json"), bytes.Replace(pb, []byte(`"session": "a"`), []byte(`"session": "victim"`), 1), 0o600)
	f.refused(d, "not an intact instance plan")
	// Garbage plan.
	d = f.cloneRun(runName(plan.Hash, "garbage"))
	_ = os.WriteFile(filepath.Join(d, "plan.json"), []byte("{"), 0o600)
	f.refused(d, "not an intact instance plan")

	// A self-consistent forged plan (validly re-hashed) that adds the victim,
	// swapped into the run directory: refused, because the directory names
	// the original plan's hash.
	forged := *plan
	forged.Sessions = append(append([]piagent.SessionPlanItem{}, plan.Sessions...),
		piagent.SessionPlanItem{Session: "victim", Action: piagent.ActionAssignSessionID, InstanceID: fxVicID, Path: f.wt("victim")})
	forged.Hash = ""
	fb, _ := json.Marshal(forged)
	var tmp piagent.InstancePlan
	_ = json.Unmarshal(fb, &tmp)
	d = f.cloneRun(runName(plan.Hash, "swapped"))
	_ = os.WriteFile(filepath.Join(d, "plan.json"), mustRehash(t, &tmp), 0o600)
	f.refused(d, "but the directory belongs to plan")

	// Valid plan, journal entry that is not in the plan (any position):
	// nothing at all is rolled back, not even the valid entries.
	victim := `{"kind":"agent","name":"` + fxVictim + `","instance_id":"` + fxVicID + `","basis":"marker","plan_hash":"` + plan.Hash + `"}`
	for i, js := range [][]string{
		append(append([]string{}, lines...), victim, lines[0]),
		append([]string{victim}, lines...),
		{`{"kind":"session","name":"victim","instance_id":"` + fxVicID + `"}`},
	} {
		d := f.cloneRun(runName(plan.Hash, "member"+string(rune('a'+i))))
		writeLines(t, filepath.Join(d, "journal.jsonl"), js...)
		f.refused(d, "does not match its plan")
	}
	// Entry fields altered: different id, flipped restored flag, other
	// basis, other plan hash, unknown kind, unknown field.
	for i, mut := range []func(string) string{
		func(s string) string { return regexp.MustCompile(`si_[0-9a-f]{24}`).ReplaceAllString(s, fxVicID) },
		func(s string) string {
			return strings.Replace(s, `"kind":"session"`, `"kind":"session","restored_existing_id":true`, 1)
		},
		func(s string) string { return strings.Replace(s, `"basis":"marker"`, `"basis":"owner_confirmed"`, 1) },
		func(s string) string { return strings.Replace(s, plan.Hash, "0123456789abcdef", 1) },
		func(s string) string { return strings.Replace(s, `"kind":"session"`, `"kind":"sessionx"`, 1) },
		func(s string) string { return strings.Replace(s, `{"time"`, `{"extra":1,"time"`, 1) },
	} {
		idx := 0
		if i == 2 {
			idx = 3 // an agent entry
		}
		js := append([]string{}, lines...)
		js[idx] = mut(js[idx])
		if js[idx] == lines[idx] {
			t.Fatalf("mutation %d did not change line %q", i, lines[idx])
		}
		d := f.cloneRun(runName(plan.Hash, "field"+string(rune('a'+i))))
		writeLines(t, filepath.Join(d, "journal.jsonl"), js...)
		want := "does not match its plan"
		if i == 5 {
			want = "is malformed"
		}
		f.refused(d, want)
	}
	// A malformed line in the middle (content after it) is tampering.
	d = f.cloneRun(runName(plan.Hash, "midtorn"))
	writeLines(t, filepath.Join(d, "journal.jsonl"), lines[0], `{"kind":"sess`, lines[1])
	f.refused(d, "is malformed")
	// A malformed final line that IS newline-terminated was written whole
	// by someone, not torn by a crash: refused, not silently dropped.
	d = f.cloneRun(runName(plan.Hash, "termtail"))
	writeLines(t, filepath.Join(d, "journal.jsonl"), lines[0], `{"kind":"sess`)
	f.refused(d, "is malformed")
	d = f.cloneRun(runName(plan.Hash, "unknowntail"))
	writeLines(t, filepath.Join(d, "journal.jsonl"), lines[0], strings.Replace(lines[1], `{"time"`, `{"extra":1,"time"`, 1))
	f.refused(d, "is malformed")
	f.assertApplied()
}

func mustRehash(t *testing.T, p *piagent.InstancePlan) []byte {
	t.Helper()
	// Same scheme as InstancePlan.computeHash (unexported); asserted below.
	p.Hash = ""
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	p.Hash = hex.EncodeToString(sum[:])[:16]
	if !p.VerifyHash() {
		t.Fatal("test rehash diverged from InstancePlan.computeHash")
	}
	out, _ := json.MarshalIndent(p, "", "  ")
	return out
}

func TestInstanceRollbackValidAndRetry(t *testing.T) {
	f := newInstRollbackFixture(t)
	f.assertApplied()
	out, err := f.rollback(f.run)
	if err != nil || !strings.Contains(out, "Rolled back 4 change(s)") {
		t.Fatalf("valid rollback: %v\n%s", err, out)
	}
	s := f.sessions()
	if s["a"].InstanceID != "" || s["b"].InstanceID != "" || f.agent(fxAgentA).SessionInstanceID != "" || f.agent(fxAgentB).SessionInstanceID != "" {
		t.Fatal("run's writes not undone")
	}
	if s["a"].LocalOnly == nil || s["a"].LocalOnly.AgentID != fxAgentA {
		t.Fatal("markers must be untouched")
	}
	f.assertVictimIntact()
	// A completed rollback is not repeated without --force-older...
	f.refused(f.run, "already rolled back")
	// ...and with it, the repeat is a no-op that reports why.
	before := f.snapshot()
	instancesForceOlder = true
	out, err = f.rollback(f.run)
	instancesForceOlder = false
	if err != nil || !strings.Contains(out, "Rolled back 0 change(s)") || !strings.Contains(out, "already rolled back") {
		t.Fatalf("forced repeat: %v\n%s", err, out)
	}
	if f.snapshot() != before {
		t.Fatal("repeat changed records")
	}
}

// Security review r1: apply A, roll back A, apply B (same deterministic
// ids), lose B's run dir; re-rolling A must not undo B's writes.
func TestInstanceRollbackCompletedRunNotRepeatedOverLaterApply(t *testing.T) {
	f := newInstRollbackFixture(t)
	runA := f.run
	if _, err := f.rollback(runA); err != nil {
		t.Fatal(err)
	}
	runB := f.apply()
	f.assertApplied()
	if err := os.RemoveAll(runB); err != nil {
		t.Fatal(err)
	}
	f.refused(runA, "already rolled back")
	f.assertApplied()
}

// Two runs: an apply interrupted after its session write, then a re-apply
// (a different plan, new run dir). Newest first; the older needs
// --force-older and then removes only what is still its own.
func TestInstanceRollbackTwoRuns(t *testing.T) {
	f := newInstRollbackFixture(t)
	runA := f.run
	// Simulate A having bound only agent a: drop b's binding from A's
	// effects by unbinding b and truncating A's journal to the first three
	// (unterminated tail = torn append of b's entry).
	j, _ := os.ReadFile(filepath.Join(runA, "journal.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(j)), "\n")
	_ = os.WriteFile(filepath.Join(runA, "journal.jsonl"), []byte(strings.Join(lines[:3], "\n")+"\n"+lines[3][:10]), 0o600)
	ab := f.agent(fxAgentB)
	f.writeAgent(fxAgentB, "b", ab.CreatedAt, "", time.Time{})
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(runA, "journal.jsonl"), old, old)
	// Re-plan: only b's bind remains -> a different plan hash, run B.
	runB := f.apply()
	if filepath.Base(runB) == filepath.Base(runA) {
		t.Fatal("re-apply must use a new run dir")
	}
	f.assertApplied()
	f.refused(runA, "later migration run")
	if out, err := f.rollback(runB); err != nil || !strings.Contains(out, "Rolled back 1 change(s)") {
		t.Fatalf("rollback B: %v\n%s", err, out)
	}
	if f.agent(fxAgentB).SessionInstanceID != "" || f.agent(fxAgentA).SessionInstanceID == "" {
		t.Fatal("B must undo only its own bind")
	}
	instancesForceOlder = true
	out, err := f.rollback(runA)
	instancesForceOlder = false
	if err != nil || !strings.Contains(out, "Rolled back 3 change(s)") {
		t.Fatalf("rollback A: %v\n%s", err, out)
	}
	s := f.sessions()
	if s["a"].InstanceID != "" || s["b"].InstanceID != "" || f.agent(fxAgentA).SessionInstanceID != "" {
		t.Fatal("A's writes must be undone")
	}
	f.assertVictimIntact()
}

func TestInstanceRollbackPartialJournalAndInterruptedApply(t *testing.T) {
	f := newInstRollbackFixture(t)
	journal, _ := os.ReadFile(filepath.Join(f.run, "journal.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(journal)), "\n")
	// Crash mid-append of the last agent entry: its bind never happened
	// (journal precedes the write), so emulate by unbinding b and tearing
	// (no trailing newline, as a real torn single write would leave).
	torn := strings.Join(lines[:3], "\n") + "\n" + lines[3][:len(lines[3])/2]
	if err := os.WriteFile(filepath.Join(f.run, "journal.jsonl"), []byte(torn), 0o600); err != nil {
		t.Fatal(err)
	}
	f.writeAgent(fxAgentB, "b", time.Date(2026, 10, 1, 12, 0, 1, 0, time.UTC), "", time.Time{})
	out, err := f.rollback(f.run)
	if err != nil || !strings.Contains(out, "Rolled back 3 change(s)") {
		t.Fatalf("torn-tail rollback: %v\n%s", err, out)
	}
	if f.sessions()["b"].InstanceID != "" || f.agent(fxAgentA).SessionInstanceID != "" {
		t.Fatal("journaled writes must be undone")
	}
	f.assertVictimIntact()
}

// PR86 r4243222242: a complete, valid JSON entry WITHOUT its trailing
// newline is a torn append too (apply writes entry+"\n" in one write and
// returns before the record write unless that write fully succeeded), so
// its step never happened in this run and must not be undone: a later run
// may have written the same deterministic value.
func TestInstanceRollbackDropsCompleteButUnterminatedTail(t *testing.T) {
	f := newInstRollbackFixture(t)
	runA := f.run
	journal := mustRead(t, filepath.Join(runA, "journal.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(journal)), "\n")
	if !strings.Contains(lines[3], `"name":"`+fxAgentB+`"`) {
		t.Fatalf("expected agent b last: %s", lines[3])
	}
	// Run A crashed with b's full entry persisted but not its newline: b
	// was never bound by A.
	if err := os.WriteFile(filepath.Join(runA, "journal.jsonl"), []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	ab := f.agent(fxAgentB)
	f.writeAgent(fxAgentB, "b", ab.CreatedAt, "", time.Time{})
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(runA, "journal.jsonl"), old, old)
	// Run B binds b (same deterministic value); its run dir is later lost.
	runB := f.apply()
	f.assertApplied()
	bound := f.agent(fxAgentB)
	if err := os.RemoveAll(runB); err != nil {
		t.Fatal(err)
	}
	out, err := f.rollback(runA)
	if err != nil || !strings.Contains(out, "Rolled back 3 change(s)") {
		t.Fatalf("rollback A: %v\n%s", err, out)
	}
	if got := f.agent(fxAgentB); got.SessionInstanceID != bound.SessionInstanceID || !got.SessionCreatedAt.Equal(bound.SessionCreatedAt) {
		t.Fatalf("rollback of A removed run B's binding of %s (A's unterminated step never happened)", fxAgentB)
	}
	if f.agent(fxAgentA).SessionInstanceID != "" || f.sessions()["a"].InstanceID != "" {
		t.Fatal("A's committed (newline-terminated) steps must still be undone")
	}
	f.assertVictimIntact()
}

// Journal tail classification: newline-terminated valid lines are steps;
// any unterminated final line (valid or invalid JSON) is dropped; a
// newline-terminated invalid line is refused.
func TestParseInstanceJournalTails(t *testing.T) {
	valid := `{"time":"2026-10-01T12:00:00Z","kind":"session","name":"a","instance_id":"si_aaaaaaaaaaaaaaaaaaaaaaaa"}`
	valid2 := strings.Replace(valid, `"name":"a"`, `"name":"b"`, 1)
	for _, c := range []struct {
		name, data string
		want       int
		err        bool
	}{
		{"terminated valid", valid + "\n" + valid2 + "\n", 2, false},
		{"terminated valid, trailing blank lines", valid + "\n" + valid2 + "\n\n \n", 2, false},
		{"terminated valid, CRLF", valid + "\r\n" + valid2 + "\r\n", 2, false},
		{"unterminated valid tail", valid + "\n" + valid2, 1, false},
		{"unterminated valid tail with trailing spaces", valid + "\n" + valid2 + "  ", 1, false},
		{"unterminated invalid tail", valid + "\n" + valid2[:20], 1, false},
		{"single unterminated valid line", valid, 0, false},
		{"empty", "", 0, false},
		{"terminated invalid tail", valid + "\n" + valid2[:20] + "\n", 0, true},
		{"invalid middle line", valid + "\n{\n" + valid2 + "\n", 0, true},
		{"terminated line with trailing garbage", valid + "}\n", 0, true},
		{"terminated line with two values", valid + valid2 + "\n", 0, true},
	} {
		got, err := parseInstanceJournal([]byte(c.data))
		if (err != nil) != c.err || (!c.err && len(got) != c.want) {
			t.Errorf("%s: got %d entries, err=%v; want %d, err=%v", c.name, len(got), err, c.want, c.err)
		}
	}
}

func TestInstanceRollbackInterruptedSessionWrite(t *testing.T) {
	// Session entries journaled but the sessions write never landed (crash
	// between journal and replace), so no agent was bound either: restore
	// the pre-apply records from the run's own backup, then roll back. Every
	// entry is skipped by value; nothing is written.
	f := newInstRollbackFixture(t)
	for _, p := range []struct{ src, dst string }{
		{filepath.Join(f.run, "sessions.json"), filepath.Join(f.cfg, "sessions.json")},
		{filepath.Join(f.run, "agents", fxAgentA, "agent.json"), f.agentPath(fxAgentA)},
		{filepath.Join(f.run, "agents", fxAgentB, "agent.json"), f.agentPath(fxAgentB)},
	} {
		b, err := os.ReadFile(p.src)
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(p.dst, b, 0o600)
	}
	before := f.snapshot()
	out, err := f.rollback(f.run)
	if err != nil || !strings.Contains(out, "Rolled back 0 change(s)") || !strings.Contains(out, "write never happened") || !strings.Contains(out, "bind never happened") {
		t.Fatalf("journaled-but-unwritten must be skipped: %v\n%s", err, out)
	}
	if f.snapshot() != before {
		t.Fatal("nothing may be written")
	}
}

func TestInstanceRollbackLeavesChangedRecords(t *testing.T) {
	f := newInstRollbackFixture(t)
	s := f.sessions()
	idA, idB := s["a"].InstanceID, s["b"].InstanceID
	// Session a recreated (new created_at) but somehow holding the same id;
	// agent b re-pointed at another session while keeping the instance id.
	if err := (&session.SessionStore{}).Mutate(func(st *session.SessionStore) error {
		st.Sessions["a"].CreatedAt = st.Sessions["a"].CreatedAt.Add(time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ab := f.agent(fxAgentB)
	f.writeAgent(fxAgentB, "victim", ab.CreatedAt, idB, ab.SessionCreatedAt)
	out, err := f.rollback(f.run)
	if err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if !strings.Contains(out, "session a: not the record this run wrote") || !strings.Contains(out, "agent "+fxAgentB+": not the record this run bound") {
		t.Fatalf("changed records must be reported:\n%s", out)
	}
	s = f.sessions()
	if s["a"].InstanceID != idA || f.agent(fxAgentB).SessionInstanceID != idB {
		t.Fatal("changed records must be left alone")
	}
	if s["b"].InstanceID != "" || f.agent(fxAgentA).SessionInstanceID != "" {
		t.Fatal("unchanged records must still be undone")
	}
	f.assertVictimIntact()
}

func TestInstanceRollbackConcurrent(t *testing.T) {
	f := newInstRollbackFixture(t)
	plan := f.runPlan()
	journal, _ := os.ReadFile(filepath.Join(f.run, "journal.jsonl"))
	entries, err := parseInstanceJournal(journal)
	if err != nil {
		t.Fatal(err)
	}
	// Drive the piagent rollback concurrently against the real stores;
	// the per-record value checks under locks make every value removed once.
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, _, err := piagent.RollbackInstancePlan(plan, entries, instanceStores(nil))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				t.Error(err)
			}
			total += n
		}()
	}
	wg.Wait()
	if total != 4 {
		t.Fatalf("each of the 4 writes must be undone exactly once, total=%d", total)
	}
	f.assertVictimIntact()
}

func TestInstanceRollbackRefusesUnknownHome(t *testing.T) {
	f := newInstRollbackFixture(t)
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "") // plan9
	if _, err := f.rollback(f.run); err == nil || !strings.Contains(err.Error(), "cannot determine the home directory") {
		t.Fatalf("unknown home must be refused, got %v", err)
	}
}

// An interrupted rollback (failed after removing some values, so no
// rolled-back marker) finishes when re-run.
func TestInstanceRollbackInterruptedIsRetryable(t *testing.T) {
	f := newInstRollbackFixture(t)
	// Interrupt: remove agent a's binding exactly as rollback would, then
	// stop (no marker, other values still present).
	plan := f.runPlan()
	entries, err := parseInstanceJournal(mustRead(t, filepath.Join(f.run, "journal.jsonl")))
	if err != nil {
		t.Fatal(err)
	}
	var first []piagent.JournalEntry
	for _, e := range entries {
		if e.Kind == "agent" && e.Name == fxAgentA {
			first = append(first, e)
		}
	}
	if n, _, err := piagent.RollbackInstancePlan(plan, first, instanceStores(nil)); err != nil || n != 1 {
		t.Fatalf("partial: %d %v", n, err)
	}
	out, err := f.rollback(f.run)
	if err != nil || !strings.Contains(out, "Rolled back 3 change(s)") || !strings.Contains(out, "agent "+fxAgentA+": not bound") {
		t.Fatalf("retry must finish: %v\n%s", err, out)
	}
	s := f.sessions()
	if s["a"].InstanceID != "" || s["b"].InstanceID != "" || f.agent(fxAgentB).SessionInstanceID != "" {
		t.Fatal("retry left values behind")
	}
	f.assertVictimIntact()
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInstanceRollbackNewerRunEqualMtime(t *testing.T) {
	f := newInstRollbackFixture(t)
	newer := f.cloneRun("session-instances-20261001T000000Z-" + f.runPlan().Hash + "-twin")
	mt := time.Now().Add(-time.Minute)
	_ = os.Chtimes(filepath.Join(f.run, "journal.jsonl"), mt, mt)
	_ = os.Chtimes(filepath.Join(newer, "journal.jsonl"), mt, mt)
	f.refused(f.run, "later migration run")
}
