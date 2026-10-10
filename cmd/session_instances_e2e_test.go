package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/internal/tmuxfixture"
)

// TestSessionInstancesMigrationEndToEnd drives the real binary against a fake
// HOME holding pre-instance-id records: the dry run is read-only (no file is
// created or changed) and already shows the exact applicable plan hash;
// unmarked agents are candidates that bind only with --confirm; --apply
// requires the reviewed hash, backs up, journals, is idempotent, and
// --rollback restores exactly the previous field values.
func TestSessionInstancesMigrationEndToEnd(t *testing.T) {
	w := tmuxfixture.ActiveWrapper()
	if w == nil || os.Getenv("HOME") != w.Home {
		t.Skip("isolated test HOME not active")
	}
	home, err := os.MkdirTemp(w.Home, "instances-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, _ := filepath.Abs("..")
	bin := filepath.Join(home, "bin", "devx")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build devx: %v\n%s", err, out)
	}
	cfgDir := filepath.Join(home, ".config", "devx")
	_ = os.MkdirAll(cfgDir, 0o700)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("disable_caddy: true\nauto_check_updates: false\nweb_autostart: false\n"), 0o600)
	env := append(os.Environ(), "DEVX_DISABLE_CADDY=true", "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"))
	run := func(args ...string) (string, error) {
		c := exec.Command(bin, append([]string{"session", "instances"}, args...)...)
		c.Env, c.Dir = env, home
		out, err := c.CombinedOutput()
		return string(out), err
	}

	t0 := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
	sessions := map[string]any{}
	writeAgent := func(id, sess, wt string, created time.Time) {
		dir := filepath.Join(cfgDir, "pi-agents", "agents", id)
		_ = os.MkdirAll(dir, 0o700)
		rec := map[string]any{"id": id, "devx_session": sess, "project": "synth", "worktree": wt, "pi_session_id": "x",
			"binding": map[string]any{"tmux_session": sess}, "lease": map[string]any{"holder": "human", "generation": 1},
			"created_at": created, "updated_at": created}
		b, _ := json.Marshal(rec)
		_ = os.WriteFile(filepath.Join(dir, "agent.json"), b, 0o600)
	}
	sess := func(name string, created time.Time, extra map[string]any) {
		m := map[string]any{"name": name, "project_alias": "synth", "branch": name, "path": filepath.Join(home, "wt", name),
			"ports": map[string]int{}, "created_at": created, "updated_at": created}
		for k, v := range extra {
			m[k] = v
		}
		sessions[name] = m
	}
	// marked: local-only marker; legacy: no marker, older than agent;
	// recreated: no marker, NEWER than agent (ambiguous -> skipped);
	// human: plain session, no agent.
	sess("marked", t0, map[string]any{"local_only": map[string]any{"owner": "devx-pi-mcp", "agent_id": "pa_000000000001", "created_at": t0}})
	writeAgent("pa_000000000001", "marked", filepath.Join(home, "wt", "marked"), t0.Add(50*time.Millisecond))
	sess("legacy", t0, nil)
	writeAgent("pa_000000000002", "legacy", filepath.Join(home, "wt", "legacy"), t0.Add(70*time.Millisecond))
	sess("recreated", t0.Add(time.Hour), nil)
	writeAgent("pa_000000000003", "recreated", filepath.Join(home, "wt", "recreated"), t0)
	sess("human", t0, map[string]any{"pinned": true, "display_name": "Human"})
	sb, _ := json.MarshalIndent(map[string]any{"sessions": sessions}, "", "  ")
	sessPath := filepath.Join(cfgDir, "sessions.json")
	_ = os.WriteFile(sessPath, sb, 0o600)
	hash := func(p string) string {
		b, _ := os.ReadFile(p)
		h := sha256.Sum256(b)
		return hex.EncodeToString(h[:])
	}
	agentPath := func(id string) string { return filepath.Join(cfgDir, "pi-agents", "agents", id, "agent.json") }
	before := map[string]string{"sessions": hash(sessPath)}
	for _, id := range []string{"pa_000000000001", "pa_000000000002", "pa_000000000003"} {
		before[id] = hash(agentPath(id))
	}
	unchanged := func(label string) {
		t.Helper()
		if hash(sessPath) != before["sessions"] {
			t.Fatalf("%s: sessions.json changed", label)
		}
		for id, h := range before {
			if id != "sessions" && hash(agentPath(id)) != h {
				t.Fatalf("%s: %s changed", label, id)
			}
		}
	}

	listFiles := func() string {
		var fs []string
		_ = filepath.Walk(cfgDir, func(p string, info os.FileInfo, err error) error {
			if err == nil {
				fs = append(fs, p+"|"+info.Mode().String())
			}
			return nil
		})
		return strings.Join(fs, "\n")
	}
	tree := listFiles()
	// 1. Dry run: read-only (no file created, not even a key or lock) and
	// shows the exact applicable hash. The unmarked agent is a candidate.
	out, err := run()
	if err != nil || !strings.Contains(out, "read-only, nothing written") {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	unchanged("dry run")
	if listFiles() != tree {
		t.Fatal("dry run must not create or change any file")
	}
	m := regexp.MustCompile(`Plan hash: ([0-9a-f]{16})`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no plan hash:\n%s", out)
	}
	if out2, _ := run(); !strings.Contains(out2, m[1]) {
		t.Fatal("plan hash must be stable across dry runs")
	}
	if !regexp.MustCompile(`pa_000000000002\s+legacy\s+candidate_needs_owner_confirmation`).MatchString(out) {
		t.Fatalf("unmarked agent must be an owner-confirmation candidate:\n%s", out)
	}
	if !strings.Contains(out, "pa_000000000003") || !strings.Contains(out, "newer than the agent") {
		t.Fatalf("recreated case must be reported as skipped:\n%s", out)
	}
	// 2. Owner confirms the candidate: a different, still read-only plan.
	if out, err := run("--confirm", "pa_000000000003"); err != nil || !strings.Contains(out, "NOT CANDIDATES") {
		t.Fatalf("confirming a skipped agent must be reported: %v\n%s", err, out)
	}
	if out, err := run("--apply", m[1], "--confirm", "pa_000000000003"); err == nil || !strings.Contains(out, "not candidates") {
		t.Fatalf("apply with a bad confirmation must be refused: %v\n%s", err, out)
	}
	unchanged("bad confirm")
	out, err = run("--confirm", "pa_000000000002")
	if err != nil {
		t.Fatalf("confirm dry run: %v\n%s", err, out)
	}
	unchanged("confirm dry run")
	m = regexp.MustCompile(`Plan hash: ([0-9a-f]{16})`).FindStringSubmatch(out)
	planHash := m[1]
	if !strings.Contains(out, "--apply "+planHash+" --confirm pa_000000000002") {
		t.Fatalf("apply hint must carry the confirmation:\n%s", out)
	}
	// The confirmed hash is not valid without the same confirmation.
	if out, err := run("--apply", planHash); err == nil || !strings.Contains(out, "does not match") {
		t.Fatalf("confirmed plan hash must not apply without --confirm: %v\n%s", err, out)
	}
	unchanged("hash without confirm")
	// 3. Wrong hash: refused, nothing written.
	if out, err := run("--apply", "0000000000000000"); err == nil || !strings.Contains(out, "does not match") {
		t.Fatalf("wrong hash must be refused: %v\n%s", err, out)
	}
	unchanged("wrong hash")
	// 4. Apply the reviewed hash.
	out, err = run("--apply", planHash, "--confirm", "pa_000000000002")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	bm := regexp.MustCompile(`--rollback (\S+)`).FindStringSubmatch(out)
	if bm == nil {
		t.Fatalf("no rollback hint:\n%s", out)
	}
	backup := bm[1]
	for _, f := range []string{"sessions.json", "plan.json", "journal.jsonl", "agents/pa_000000000001/agent.json", "agents/pa_000000000002/agent.json"} {
		if _, err := os.Stat(filepath.Join(backup, f)); err != nil {
			t.Fatalf("backup missing %s", f)
		}
	}
	if hash(filepath.Join(backup, "sessions.json")) != before["sessions"] {
		t.Fatal("backup must be the pre-apply sessions.json")
	}
	var after struct {
		Sessions map[string]map[string]any `json:"sessions"`
	}
	b, _ := os.ReadFile(sessPath)
	_ = json.Unmarshal(b, &after)
	for _, n := range []string{"marked", "legacy", "recreated", "human"} {
		if id, _ := after.Sessions[n]["instance_id"].(string); !regexp.MustCompile(`^si_[0-9a-f]{24}$`).MatchString(id) {
			t.Fatalf("%s instance_id %q", n, id)
		}
	}
	if after.Sessions["human"]["pinned"] != true || after.Sessions["human"]["display_name"] != "Human" {
		t.Fatal("other fields must be preserved")
	}
	readAgent := func(id string) map[string]any {
		var a map[string]any
		b, _ := os.ReadFile(agentPath(id))
		_ = json.Unmarshal(b, &a)
		return a
	}
	if readAgent("pa_000000000001")["session_instance_id"] != after.Sessions["marked"]["instance_id"] ||
		readAgent("pa_000000000002")["session_instance_id"] != after.Sessions["legacy"]["instance_id"] {
		t.Fatal("unambiguous agents must be bound to their session's id")
	}
	if _, ok := readAgent("pa_000000000003")["session_instance_id"]; ok {
		t.Fatal("ambiguous (recreated) agent must not be bound")
	}
	// 5. Idempotent: nothing left to apply; same hash re-apply is a no-op.
	out, _ = run()
	if !strings.Contains(out, "Nothing to apply") {
		t.Fatalf("second plan should be empty:\n%s", out)
	}
	// 5b. A wrong-hash or no-op apply creates no journal; two backups are
	// distinct directories even within one second.
	if out, _ := run(); strings.Contains(out, "--apply ") {
		t.Fatalf("no further apply should be offered:\n%s", out)
	}
	// 5c. An older run cannot be rolled back over a newer one without
	// --force-older.
	fake := filepath.Join(filepath.Dir(backup), "session-instances-99990101T000000Z-x-newer")
	_ = os.MkdirAll(fake, 0o700)
	_ = os.WriteFile(filepath.Join(fake, "journal.jsonl"), []byte("{}\n"), 0o600)
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(fake, "journal.jsonl"), future, future)
	if out, err := run("--rollback", backup); err == nil || !strings.Contains(out, "later migration run") {
		t.Fatalf("rollback of an older run must be refused: %v\n%s", err, out)
	}
	_ = os.RemoveAll(fake)
	// 6. Rollback restores the original field values exactly.
	out, err = run("--rollback", backup)
	if err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	var rolled, orig map[string]any
	b, _ = os.ReadFile(sessPath)
	_ = json.Unmarshal(b, &rolled)
	_ = json.Unmarshal(sb, &orig)
	for n := range sessions {
		if _, ok := rolled["sessions"].(map[string]any)[n].(map[string]any)["instance_id"]; ok {
			t.Fatalf("%s still has instance_id after rollback", n)
		}
	}
	for _, id := range []string{"pa_000000000001", "pa_000000000002"} {
		if _, ok := readAgent(id)["session_instance_id"]; ok {
			t.Fatalf("%s still bound after rollback", id)
		}
	}
	if _, err := os.Stat(filepath.Join(backup, "rolled-back")); err != nil {
		t.Fatal("rolled-back marker missing")
	}
	fmt.Fprintln(os.Stderr, "instances e2e ok")
}
