package cmd

import (
	"bufio"
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

// TestAgentCLIAndMCPEndToEnd builds the real devx binary and drives it the
// way a user and an MCP client would, against a fresh fixture:
//
//   - HOME / XDG dirs are the guard's fake HOME (TestMain), with a freshly
//     written fixture config (no web token/port, caddy/updates/web/usage
//     off) and a synthetic project allowlist;
//   - every tmux exec (devx, tmuxp, Pi bridge's devx calls) goes through the
//     owned-socket wrapper; humans are simulated with send-keys on the same
//     owned socket;
//   - Pi runs with an isolated agent dir, no user extensions/skills/MCP, and
//     a deterministic local faux model.
//
// Skipped unless pi, tmuxp and git are available and DEVX_CLI_E2E != 0.
func TestAgentCLIAndMCPEndToEnd(t *testing.T) {
	if os.Getenv("DEVX_CLI_E2E") == "0" {
		t.Skip("DEVX_CLI_E2E=0")
	}
	w := tmuxfixture.ActiveWrapper()
	if w == nil || w.RealTmux == "" || os.Getenv("HOME") != w.Home {
		t.Skip("owned tmux wrapper / fake HOME not active")
	}
	piBin := os.Getenv("DEVX_PI_BIN")
	if piBin == "" {
		piBin = findRealPi()
	}
	for _, b := range []string{"tmuxp", "git"} {
		if _, err := exec.LookPath(b); err != nil {
			t.Skip(b + " not available")
		}
	}
	if _, err := os.Stat(piBin); err != nil {
		t.Skip("pi not available")
	}
	// Per-test HOME inside the guard's fake HOME, so this test's config and
	// project registry never leak into other cmd tests (which share the
	// guard HOME through viper).
	home, err := os.MkdirTemp(w.Home, "cli-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, _ := filepath.Abs("..")
	faux := filepath.Join(repoRoot, "piagent", "testdata", "faux-model.ts")

	// Build the real binary into the fixture.
	bin := filepath.Join(home, "bin", "devx")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build devx: %v\n%s", err, out)
	}

	// Synthetic project repo + fixture config.
	repo := filepath.Join(home, "repo")
	mustRun(t, "", "git", "init", "-q", "-b", "main", repo)
	mustRun(t, repo, "git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	cfgDir := filepath.Join(home, ".config", "devx")
	_ = os.MkdirAll(cfgDir, 0o700)
	piDir := filepath.Join(home, "pi-agent-dir")
	_ = os.MkdirAll(piDir, 0o700)
	piWrap := filepath.Join(home, "bin", "pi-synth")
	_ = os.WriteFile(piWrap, []byte(fmt.Sprintf("#!/bin/sh\nexec env PI_CODING_AGENT_DIR=%s GATEPOST_HOST_DISABLE=1 PI_OFFLINE=1 %s --offline --no-extensions --no-skills --no-context-files --no-mcp --no-approve --no-prompt-templates -e %s --model devx-faux/faux-1 \"$@\"\n", piDir, piBin, faux)), 0o700)
	tmpl := filepath.Join(cfgDir, "session.yaml.tmpl")
	_ = os.WriteFile(tmpl, []byte("session_name: {{.Name}}\nstart_directory: {{.Path}}\noptions:\n  base-index: 1\nwindows:\n  - window_name: shell\n    panes:\n      - echo human shell\n"), 0o600)
	cfg := fmt.Sprintf("basedomain: localhost\ndisable_caddy: true\ntarget: host\nports: []\nauto_check_updates: false\nweb_autostart: false\nusage:\n  enabled: false\ntmuxp_template: %s\npi_mcp:\n  allowed_projects: [synth]\n  pi_command: %s\n", tmpl, piWrap)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o600)

	env := append(os.Environ(), "DEVX_DISABLE_CADDY=true", "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"))
	devx := func(args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = home
		c.Env = env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	if out, err := devx("project", "add", repo, "--alias", "synth"); err != nil {
		t.Fatalf("project add: %v\n%s", err, out)
	}

	// --- MCP over stdio, one process per call (connection drops between). ---
	n := 0
	mcp := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		n++
		a, _ := json.Marshal(args)
		in := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}
{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}
`, n+1, tool, a)
		c := exec.Command(bin, "mcp", "pi")
		c.Dir = home
		c.Env = env
		c.Stdin = strings.NewReader(in)
		out, err := c.Output()
		if err != nil {
			t.Fatalf("mcp %s: %v", tool, err)
		}
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		sc.Buffer(make([]byte, 1<<20), 1<<22)
		for sc.Scan() {
			var r map[string]any
			if json.Unmarshal(sc.Bytes(), &r) == nil && r["id"] == float64(n+1) {
				res := r["result"].(map[string]any)
				sc := res["structuredContent"].(map[string]any)
				if res["isError"] == true {
					sc["_isError"] = true
				}
				return sc
			}
		}
		t.Fatalf("no response for %s: %s", tool, out)
		return nil
	}
	taskState := func(id string) map[string]any {
		return mcp("pi_status", map[string]any{"task_id": id})["task"].(map[string]any)
	}
	waitState := func(id string, timeout time.Duration, want ...string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(timeout)
		var st map[string]any
		for time.Now().Before(deadline) {
			st = taskState(id)
			for _, s := range want {
				if st["state"] == s {
					return st
				}
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Fatalf("task %s not in %v: %v", id, want, st)
		return nil
	}

	// Start, then retry with the same key (response lost). Same IDs.
	start := map[string]any{"project": "synth", "session_name": "cli-e2e", "prompt": "remember PELICAN", "idempotency_key": "start-1"}
	r1 := mcp("pi_start_task", start)
	if r1["_isError"] == true {
		t.Fatalf("start: %v", r1)
	}
	r2 := mcp("pi_start_task", start)
	if r1["agent_id"] != r2["agent_id"] || r1["task_id"] != r2["task_id"] || r2["replayed"] != true {
		t.Fatalf("start retry not idempotent: %v %v", r1, r2)
	}
	agent := r1["agent_id"].(string)
	waitState(r1["task_id"].(string), 60*time.Second, "completed")

	// The agent is a visible DevX session with a pi-agent window.
	if out, err := devx("session", "list"); err != nil || !strings.Contains(out, "cli-e2e") {
		t.Fatalf("session not visible: %v %s", err, out)
	}
	if out, err := devx("agent", "status", agent); err != nil || !strings.Contains(out, "State:       idle") {
		t.Fatalf("agent status: %v %s", err, out)
	}

	// Remote SLOW task; human takes over via CLI while it runs, then types.
	s1 := mcp("pi_send", map[string]any{"agent_id": agent, "prompt": "long SLOW", "idempotency_key": "s1"})
	waitState(s1["task_id"].(string), 30*time.Second, "running")
	q := mcp("pi_send", map[string]any{"agent_id": agent, "prompt": "queued after takeover", "idempotency_key": "s2"})
	if out, err := devx("agent", "takeover", agent); err != nil {
		t.Fatalf("takeover: %v %s", err, out)
	}
	paneOut, _ := devx("agent", "status", agent, "--json")
	pane := regexp.MustCompile(`"pane_id": "(%\d+)"`).FindStringSubmatch(paneOut)
	if pane == nil {
		t.Fatalf("no pane in status: %s", paneOut)
	}
	if _, err := exec.Command("tmux", "send-keys", "-t", pane[1], "-l", "human typed here").CombinedOutput(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	_, _ = exec.Command("tmux", "send-keys", "-t", pane[1], "Enter").CombinedOutput()
	st := waitState(s1["task_id"].(string), 60*time.Second, "completed", "cancelled")
	if st["human_intervened"] != true {
		t.Fatalf("remote task should record human intervention: %v", st)
	}
	time.Sleep(1500 * time.Millisecond)
	qs := taskState(q["task_id"].(string))
	if qs["state"] != "waiting" || qs["waiting_reason"] != "human_control" {
		t.Fatalf("queued task not held for human: %v", qs)
	}
	// Remote cancel of the human's running work is denied while human holds control.
	inspect, _ := devx("agent", "inspect", agent, "--lines", "80")
	if !strings.Contains(inspect, "human typed here") || strings.Contains(inspect, "queued after takeover") {
		t.Fatalf("pane content wrong under human control:\n%s", inspect)
	}
	// Release; queued task completes in the same conversation.
	if out, err := devx("agent", "release", agent); err != nil {
		t.Fatalf("release: %v %s", err, out)
	}
	done := waitState(q["task_id"].(string), 60*time.Second, "completed")
	if !strings.Contains(fmt.Sprint(done["result_excerpt"]), "queued after takeover") {
		t.Fatalf("released task result: %v", done)
	}

	// Events: full read, then replay from a cursor in a new connection.
	all := mcp("pi_events", map[string]any{"agent_id": agent, "after_seq": 0, "limit": 200})
	evs := all["events"].([]any)
	if len(evs) < 10 {
		t.Fatalf("too few events: %d", len(evs))
	}
	mid := evs[4].(map[string]any)["seq"].(float64)
	again := mcp("pi_events", map[string]any{"agent_id": agent, "after_seq": mid, "limit": 3})["events"].([]any)
	if len(again) != 3 || again[0].(map[string]any)["seq"].(float64) != mid+1 {
		t.Fatalf("replay from cursor: %v", again)
	}

	// Permission denial: unallowlisted project, existing session.
	if d := mcp("pi_start_task", map[string]any{"project": "real-project", "prompt": "x", "idempotency_key": "d1"}); d["error"] != "permission_denied" {
		t.Fatalf("not denied: %v", d)
	}
	if d := mcp("pi_start_task", map[string]any{"project": "synth", "session_name": "cli-e2e", "prompt": "x", "idempotency_key": "d2"}); d["error"] != "permission_denied" {
		t.Fatalf("existing session not denied: %v", d)
	}

	// Pi crash -> unknown -> relaunch restores the same conversation.
	run := mcp("pi_send", map[string]any{"agent_id": agent, "prompt": "crash during SLOW", "idempotency_key": "s3"})
	waitState(run["task_id"].(string), 30*time.Second, "running")
	statusJSON, _ := devx("agent", "status", agent, "--json")
	pid := regexp.MustCompile(`"pid": (\d+)`).FindStringSubmatch(statusJSON)
	if pid == nil {
		t.Fatalf("no bridge pid: %s", statusJSON)
	}
	// SIGKILL only the fixture Pi process this test launched (pid read from
	// its own bridge heartbeat in the fixture state dir).
	// Fail closed unless that pid is a Pi started from this fixture's
	// launch script (its command line names the fixture HOME).
	// Pi's process title hides its argv, so verify ownership through the
	// process tree: the pid must descend from the pane process of the
	// fixture-owned tmux server (queried on the owned socket).
	panePID, err := exec.Command("tmux", "display-message", "-p", "-t", pane[1], "#{pane_pid}").Output()
	if err != nil {
		t.Fatalf("pane pid: %v", err)
	}
	if !descendsFrom(pid[1], strings.TrimSpace(string(panePID))) {
		t.Fatalf("refusing to signal pid %s: not under the fixture pane", pid[1])
	}
	if err := exec.Command("kill", "-9", pid[1]).Run(); err != nil {
		t.Fatal(err)
	}
	waitState(run["task_id"].(string), 30*time.Second, "unknown")
	if out, err := devx("agent", "relaunch", agent); err != nil {
		t.Fatalf("relaunch: %v %s", err, out)
	}
	after := mcp("pi_send", map[string]any{"agent_id": agent, "prompt": "after relaunch", "idempotency_key": "s4"})
	waitState(after["task_id"].(string), 60*time.Second, "completed")
	inspect, _ = devx("agent", "inspect", agent, "--lines", "400")
	// Same conversation: pre-crash turns are shown by the resumed TUI and
	// the bridge reports the original Pi session id and file. (The very
	// first turn can scroll out of the 80x24 fixture pane, so assert on the
	// persisted session rather than on PELICAN being on screen.)
	if !strings.Contains(inspect, "queued after takeover") || !strings.Contains(inspect, "after relaunch") {
		t.Fatalf("pre-crash history not shown after relaunch:\n%s", inspect)
	}
	statusJSON, _ = devx("agent", "status", agent, "--json")
	var sv struct {
		Agent struct {
			Agent struct {
				PiSessionID string `json:"pi_session_id"`
			} `json:"agent"`
			Bridge struct {
				PiSessionID   string `json:"pi_session_id"`
				PiSessionFile string `json:"pi_session_file"`
			} `json:"bridge"`
		} `json:"agent"`
	}
	if err := json.Unmarshal([]byte(statusJSON), &sv); err != nil {
		t.Fatal(err)
	}
	if sv.Agent.Bridge.PiSessionID != r1["pi_session_id"] || sv.Agent.Agent.PiSessionID != r1["pi_session_id"] {
		t.Fatalf("pi session changed across relaunch: %+v want %v", sv, r1["pi_session_id"])
	}
	sess, err := os.ReadFile(sv.Agent.Bridge.PiSessionFile)
	if err != nil || !strings.Contains(string(sess), "remember PELICAN") {
		t.Fatalf("persisted conversation missing first turn: %v", err)
	}

	// Inert web notification: the fixture config has no web token/port, so
	// the bridge's `devx session flag` cannot reach any real web server.
	if strings.Contains(cfg, "web_secret_token") || strings.Contains(cfg, "web_port") {
		t.Fatal("fixture config must not carry web credentials")
	}
	if out, err := devx("agent", "list"); err != nil || !strings.Contains(out, agent) {
		t.Fatalf("agent list: %v %s", err, out)
	}
	if lp := os.Getenv("DEVX_CLI_E2E_LOG"); lp != "" {
		_ = os.WriteFile(lp, []byte(fmt.Sprintf("agent=%s events=%d inspect_tail=\n%s\n", agent, len(evs), tail(inspect, 25))), 0o600)
	}
}

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	c := exec.Command(name, args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func tail(s string, n int) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

// descendsFrom reports whether pid is ancestor itself or one of its
// descendants, by walking parent pids.
func descendsFrom(pid, ancestor string) bool {
	for i := 0; i < 32 && pid != "" && pid != "0" && pid != "1"; i++ {
		if pid == ancestor {
			return true
		}
		out, err := exec.Command("ps", "-o", "ppid=", "-p", pid).Output()
		if err != nil {
			return false
		}
		pid = strings.TrimSpace(string(out))
	}
	return false
}

// findRealPi returns the first `pi` on PATH that is not a wrapper shim
// (e.g. ~/.gatepost/bin/pi, which re-derives state from $HOME and would
// not start under the fixture's fake HOME). Empty if none is found.
func findRealPi() string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, "pi")
		st, err := os.Stat(p)
		if err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		if strings.Contains(filepath.ToSlash(dir), "/.gatepost/") {
			continue
		}
		return p
	}
	return ""
}
