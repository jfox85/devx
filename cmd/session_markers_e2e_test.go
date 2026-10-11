package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/internal/tmuxfixture"
	"github.com/jfox85/devx/session"
)

// TestSessionMarkersRepairEndToEnd drives the real binary against a
// SYNTHETIC fake HOME and the test-owned tmux socket: an MCP-created agent
// whose session record lost its local-only marker (as an older-schema writer
// does), a backup that still holds it, the agent's own creation event and a
// tmux session tagged at creation. It checks the dry run is read-only, the
// candidate needs --confirm, untrusted backups are refused, --apply needs the
// reviewed hash and refuses inside a managed agent, apply writes only
// local_only (preserving unknown fields), and --rollback undoes it exactly.
func TestSessionMarkersRepairEndToEnd(t *testing.T) {
	w := tmuxfixture.ActiveWrapper()
	if w == nil || os.Getenv("HOME") != w.Home {
		t.Skip("isolated test HOME not active")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	home, err := os.MkdirTemp(w.Home, "markers-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, _ := filepath.Abs("..")
	bin := filepath.Join(home, "bin", "devx"+exeSuffix())
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build devx: %v\n%s", err, out)
	}
	cfgDir := filepath.Join(home, ".config", "devx")
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("disable_caddy: true\nauto_check_updates: false\nweb_autostart: false\n"), 0o600)
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "DEVX_PI_AGENT_ID=") {
			env = append(env, kv)
		}
	}
	env = append(env, "DEVX_DISABLE_CADDY=true", "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	run := func(extraEnv []string, args ...string) (string, error) {
		c := exec.Command(bin, append([]string{"session", "markers"}, args...)...)
		c.Env, c.Dir = append(append([]string{}, env...), extraEnv...), home
		out, err := c.CombinedOutput()
		return string(out), err
	}

	const agentID = "pa_00000000c0de"
	const name = "mk-e2e-core"
	sc := time.Date(2026, 10, 8, 22, 32, 42, 117391000, time.FixedZone("", -7*3600))
	wt := filepath.Join(home, "proj", ".worktrees", name)
	_ = os.MkdirAll(wt, 0o700)
	record := func(withMarker bool) map[string]any {
		m := map[string]any{"name": name, "project_alias": "synth", "branch": name, "path": wt, "ports": map[string]int{},
			"created_at": sc, "updated_at": sc, "future_field": map[string]any{"keep": true}}
		if withMarker {
			m["local_only"] = map[string]any{"owner": "devx-pi-mcp", "agent_id": agentID, "created_at": sc}
		}
		return m
	}
	human := map[string]any{"name": "human", "branch": "h", "path": filepath.Join(home, "h"), "ports": map[string]int{}, "created_at": sc, "updated_at": sc, "pinned": true}
	writeStore := func(path string, withMarker bool, mode os.FileMode) []byte {
		b, _ := json.MarshalIndent(map[string]any{"sessions": map[string]any{name: record(withMarker), "human": human}}, "", "  ")
		if err := os.WriteFile(path, b, mode); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(path, mode)
		return b
	}
	sessPath := filepath.Join(cfgDir, "sessions.json")
	orig := writeStore(sessPath, false, 0o600)
	bdir := filepath.Join(home, "reviewed")
	_ = os.MkdirAll(bdir, 0o700)
	backup := filepath.Join(bdir, "sessions.json")
	writeStore(backup, true, 0o600)

	adir := filepath.Join(cfgDir, "pi-agents", "agents", agentID)
	_ = os.MkdirAll(adir, 0o700)
	agentCreated := sc.Add(59 * time.Millisecond).UTC()
	ab, _ := json.Marshal(map[string]any{"id": agentID, "devx_session": name, "project": "synth", "worktree": wt, "pi_session_id": "x",
		"binding": map[string]any{"tmux_session": name}, "lease": map[string]any{"holder": "human", "generation": 1},
		"created_at": agentCreated, "updated_at": agentCreated, "created_by": "mcp", "launch_count": 1})
	_ = os.WriteFile(filepath.Join(adir, "agent.json"), ab, 0o600)
	ev, _ := json.Marshal(map[string]any{"seq": 1, "time": agentCreated, "type": "agent_created", "source": "devx", "data": map[string]any{"session": name, "pi_session_id": "x"}})
	_ = os.WriteFile(filepath.Join(adir, "events.jsonl"), append(ev, '\n'), 0o600)
	_ = os.WriteFile(filepath.Join(adir, "seq"), []byte("1"), 0o600)

	hash := func(p string) string {
		b, _ := os.ReadFile(p)
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:])
	}
	agentPath := filepath.Join(adir, "agent.json")
	before := hash(sessPath)
	agentBefore := hash(agentPath)
	unchanged := func(label string) {
		t.Helper()
		if hash(sessPath) != before {
			t.Fatalf("%s: sessions.json changed", label)
		}
		if hash(agentPath) != agentBefore {
			t.Fatalf("%s: agent.json changed", label)
		}
	}
	hashRe := regexp.MustCompile(`Plan hash: ([0-9a-f]{16})`)

	// 0. Without the live tmux tag, nothing is proposed.
	out, err := run(nil, "--backup", backup)
	if err != nil || !strings.Contains(out, "no live tmux session") {
		t.Fatalf("missing tmux must be reported: %v\n%s", err, out)
	}
	// Tag the synthetic tmux session the way the local-only creator does.
	if o, err := exec.Command("tmux", "new-session", "-d", "-s", name, "-e", "DEVX_LOCAL_ONLY_AGENT="+agentID, "sleep", "600").CombinedOutput(); err != nil {
		t.Fatalf("tmux: %v %s", err, o)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "="+name).Run() })

	// 1. Dry run: read-only; the agent is a candidate.
	out, err = run(nil, "--backup", backup)
	if err != nil || !strings.Contains(out, "read-only, nothing written") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !regexp.MustCompile(agentID + `\s+` + name + `\s+candidate_needs_owner_confirmation`).MatchString(out) {
		t.Fatalf("expected candidate:\n%s", out)
	}
	unchanged("dry run")
	if _, err := os.Stat(filepath.Join(cfgDir, "backups")); err == nil {
		t.Fatal("dry run must not create a backups directory")
	}

	// 2. Untrusted backups are refused.
	loose := filepath.Join(bdir, "loose.json")
	writeStore(loose, true, 0o666)
	if out, err := run(nil, "--backup", loose); err == nil || !strings.Contains(out, "writable by group or others") {
		t.Fatalf("world-writable backup must be refused: %v\n%s", err, out)
	}
	link := filepath.Join(bdir, "link.json")
	_ = os.Symlink(backup, link)
	if out, err := run(nil, "--backup", link); err == nil || !strings.Contains(out, "not a regular file") {
		t.Fatalf("symlinked backup must be refused: %v\n%s", err, out)
	}
	if out, err := run(nil, "--backup", sessPath); err == nil || !strings.Contains(out, "live sessions.json") {
		t.Fatalf("live file as backup must be refused: %v\n%s", err, out)
	}

	// 3. Confirmed plan; apply needs the exact hash; refuses inside an agent.
	out, err = run(nil, "--backup", backup, "--confirm", agentID)
	if err != nil {
		t.Fatalf("confirm dry run: %v\n%s", err, out)
	}
	planHash := hashRe.FindStringSubmatch(out)[1]
	unchanged("confirm dry run")
	if out, err := run(nil, "--backup", backup, "--apply", planHash); err == nil || !strings.Contains(out, "does not match") {
		t.Fatalf("hash without the confirmation must be refused: %v\n%s", err, out)
	}
	if out, err := run([]string{"DEVX_PI_AGENT_ID=pa_000000000099"}, "--backup", backup, "--confirm", agentID, "--apply", planHash); err == nil || !strings.Contains(out, "inside a managed agent") {
		t.Fatalf("apply inside an agent must be refused: %v\n%s", err, out)
	}
	unchanged("refusals")

	// 4. Apply.
	out, err = run(nil, "--backup", backup, "--confirm", agentID, "--apply", planHash)
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	rb := regexp.MustCompile(`--rollback (\S+)`).FindStringSubmatch(out)
	if rb == nil {
		t.Fatalf("no rollback hint:\n%s", out)
	}
	if hash(filepath.Join(rb[1], "sessions.json")) != before {
		t.Fatal("pre-apply backup must equal the old sessions.json")
	}
	var after map[string]map[string]map[string]any
	b, _ := os.ReadFile(sessPath)
	_ = json.Unmarshal(b, &after)
	rec := after["sessions"][name]
	lo, _ := rec["local_only"].(map[string]any)
	if lo["agent_id"] != agentID || lo["owner"] != "devx-pi-mcp" {
		t.Fatalf("marker not restored: %v", rec)
	}
	if rec["future_field"] == nil || after["sessions"]["human"]["pinned"] != true {
		t.Fatal("unknown/other fields must be preserved")
	}
	if _, ok := rec["instance_id"]; ok {
		t.Fatal("repair must not assign an instance id")
	}
	if hash(agentPath) != agentBefore {
		t.Fatal("agent.json must not change")
	}
	if out, _ := run(nil, "--backup", backup); !strings.Contains(out, "marker_present") || !strings.Contains(out, "Nothing to apply") {
		t.Fatalf("after apply:\n%s", out)
	}
	// Re-running the same confirmed dry run after it was applied stays
	// applicable (confirmation satisfied) and has nothing left to write.
	out, err = run(nil, "--backup", backup, "--confirm", agentID)
	if err != nil || strings.Contains(out, "NOT CANDIDATES") || !strings.Contains(out, "Nothing to apply") {
		t.Fatalf("re-run after apply must be clean: %v\n%s", err, out)
	}
	if out, err := run(nil, "--backup", backup, "--confirm", agentID, "--apply", hashRe.FindStringSubmatch(out)[1]); err != nil || !strings.Contains(out, "Nothing to apply") {
		t.Fatalf("re-apply must be a no-op: %v\n%s", err, out)
	}

	appliedHash := hash(sessPath)
	// Rollback accepts only run directories it created, with a journal that
	// belongs to that run's plan, and the newest run first.
	if out, err := run(nil, "--rollback", bdir); err == nil || !strings.Contains(out, "not a marker repair run directory") {
		t.Fatalf("foreign rollback dir must be refused: %v\n%s", err, out)
	}
	fake := filepath.Join(filepath.Dir(rb[1]), "session-markers-99990101T000000Z-x-newer")
	_ = os.MkdirAll(fake, 0o700)
	_ = os.WriteFile(filepath.Join(fake, "journal.jsonl"), []byte("{}\n"), 0o600)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(fake, "journal.jsonl"), future, future)
	if out, err := run(nil, "--rollback", rb[1]); err == nil || !strings.Contains(out, "later marker repair run") {
		t.Fatalf("older run rollback must be refused: %v\n%s", err, out)
	}
	_ = os.RemoveAll(fake)
	jpath := filepath.Join(rb[1], "journal.jsonl")
	goodJournal, _ := os.ReadFile(jpath)
	_ = os.WriteFile(jpath, []byte(strings.Replace(string(goodJournal), planHash, "ffffffffffffffff", 1)), 0o600)
	if out, err := run(nil, "--rollback", rb[1]); err == nil || !strings.Contains(out, "belongs to plan") {
		t.Fatalf("journal from another plan must be refused: %v\n%s", err, out)
	}
	_ = os.WriteFile(jpath, goodJournal, 0o600)
	if hash(sessPath) != appliedHash {
		t.Fatal("refused rollbacks must not write")
	}

	// 5. Rollback restores exactly the original record.
	if out, err := run([]string{"DEVX_PI_AGENT_ID=pa_000000000099"}, "--rollback", rb[1]); err == nil || !strings.Contains(out, "inside a managed agent") {
		t.Fatalf("rollback inside an agent must be refused: %v\n%s", err, out)
	}
	if out, err := run(nil, "--rollback", rb[1]); err != nil || !strings.Contains(out, "Rolled back 1 marker") {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	// Compare through the current schema (it writes zero-valued fields the
	// hand-written fixture omitted).
	var rolled, want session.SessionStore
	b, _ = os.ReadFile(sessPath)
	if err := json.Unmarshal(b, &rolled); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(orig, &want)
	g, _ := json.Marshal(rolled)
	wnt, _ := json.Marshal(want)
	if string(g) != string(wnt) {
		t.Fatalf("rollback must restore the records:\n%s\nwant\n%s", g, wnt)
	}
}
