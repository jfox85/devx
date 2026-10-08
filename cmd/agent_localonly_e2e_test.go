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

// TestAgentLocalOnlyStartEndToEnd drives the real devx binary through the
// production local-only creator (pi_start_task over MCP stdio) against a
// project shaped like the real DevX repo: its .devx/config.yaml asks for a
// WEB service port and its session template would start an editor Pi and a
// service script. The fixture config is the dangerous one: Caddy enabled
// (fake caddy on PATH + loopback fake API), an external domain and tunnel
// id (tunnel config path inside the fixture), web autostart on.
//
// It proves that a managed start:
//   - records a local_only marker bound to the agent, with no ports/routes;
//   - generates no .envrc/.tmuxp.yaml, copies no bootstrap files, starts no
//     template windows (only devx-local + pi-agent), and never runs tmuxp,
//     caddy, cloudflared or ttyd (PATH tripwires);
//   - writes no Caddy/tunnel config, and a later default sync excludes it;
//   - opens no new TCP listener from this test's process tree;
//   - is idempotent on retry, supports inspect/takeover/release with send
//     fencing, survives a Pi crash + relaunch with the same identities.
func TestAgentLocalOnlyStartEndToEnd(t *testing.T) {
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
	if _, err := os.Stat(piBin); err != nil {
		t.Skip("pi not available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home, err := os.MkdirTemp(w.Home, "lo-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, _ := filepath.Abs("..")
	faux := filepath.Join(repoRoot, "piagent", "testdata", "faux-model.ts")
	binDir := filepath.Join(home, "bin")
	_ = os.MkdirAll(binDir, 0o700)
	bin := filepath.Join(binDir, "devx")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build devx: %v\n%s", err, out)
	}
	// Tripwires: any exec of these from devx or its children is recorded
	// and fails.
	trip := filepath.Join(home, "tripwire.log")
	for _, name := range []string{"tmuxp", "caddy", "cloudflared", "ttyd", "tailscale", "cursor", "code"} {
		_ = os.WriteFile(filepath.Join(binDir, name), []byte(fmt.Sprintf("#!/bin/sh\necho \"%s $*\" >> %s\nexit 97\n", name, trip)), 0o700)
	}

	repo := filepath.Join(home, "repo")
	mustRun(t, "", "git", "init", "-q", "-b", "main", repo)
	_ = os.MkdirAll(filepath.Join(repo, ".devx"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, ".devx", "config.yaml"), []byte("ports:\n  - WEB\nbootstrap_files:\n  - .devx-web-token\neditor: cursor\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, ".devx", "session.yaml.tmpl"), []byte("session_name: {{.Name}}\nstart_directory: {{.Path}}\nwindows:\n  - window_name: editor\n    panes:\n      - 'pi -c'\n  - window_name: services\n    panes:\n      - ./.devx/start_service.sh web\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, ".devx-web-token"), []byte("fixture-not-a-secret"), 0o600)
	mustRun(t, repo, "git", "add", ".devx")
	mustRun(t, repo, "git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")

	cfgDir := filepath.Join(home, ".config", "devx")
	_ = os.MkdirAll(cfgDir, 0o700)
	piDir := filepath.Join(home, "pi-agent-dir")
	_ = os.MkdirAll(piDir, 0o700)
	piWrap := filepath.Join(binDir, "pi-synth")
	_ = os.WriteFile(piWrap, []byte(fmt.Sprintf("#!/bin/sh\nexec env PI_CODING_AGENT_DIR=%s GATEPOST_HOST_DISABLE=1 PI_OFFLINE=1 %s --offline --no-extensions --no-skills --no-context-files --no-mcp --no-approve --no-prompt-templates -e %s --model devx-faux/faux-1 \"$@\"\n", piDir, piBin, faux)), 0o700)
	tunnelCfg := filepath.Join(home, "cloudflared.yaml")
	cfg := fmt.Sprintf("basedomain: localhost\ndisable_caddy: false\ncaddy_api: http://127.0.0.1:9\nexternal_domain: example.test\ncloudflare_tunnel_id: tunnel-test\ncloudflare_tunnel_config: %s\ntarget: host\nports: [WEB]\nauto_check_updates: false\nweb_autostart: true\nusage:\n  enabled: false\npi_mcp:\n  allowed_projects: [synth]\n  pi_command: %s\n", tunnelCfg, piWrap)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o600)

	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	listenBefore := ownListeners(t)

	n := 0
	mcp := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		n++
		a, _ := json.Marshal(args)
		in := fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"e2e\",\"version\":\"1\"}}}\n{\"jsonrpc\":\"2.0\",\"id\":%d,\"method\":\"tools/call\",\"params\":{\"name\":%q,\"arguments\":%s}}\n", n+1, tool, a)
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
				s := res["structuredContent"].(map[string]any)
				if res["isError"] == true {
					s["_isError"] = true
				}
				return s
			}
		}
		t.Fatalf("no response for %s: %s", tool, out)
		return nil
	}
	waitState := func(id string, timeout time.Duration, want ...string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(timeout)
		var st map[string]any
		for time.Now().Before(deadline) {
			st = mcp("pi_status", map[string]any{"task_id": id})["task"].(map[string]any)
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

	start := map[string]any{"project": "synth", "session_name": "lo-e2e", "prompt": "remember HERON", "idempotency_key": "lo-1"}
	r1 := mcp("pi_start_task", start)
	if r1["_isError"] == true {
		t.Fatalf("start: %v", r1)
	}
	r2 := mcp("pi_start_task", start)
	if r1["agent_id"] != r2["agent_id"] || r1["task_id"] != r2["task_id"] || r2["replayed"] != true {
		t.Fatalf("retry not idempotent: %v %v", r1, r2)
	}
	agent := r1["agent_id"].(string)
	waitState(r1["task_id"].(string), 60*time.Second, "completed")

	// Durable record: local-only marker bound to the agent, no ports/routes.
	var store struct {
		Sessions map[string]map[string]any `json:"sessions"`
	}
	raw, _ := os.ReadFile(filepath.Join(cfgDir, "sessions.json"))
	_ = json.Unmarshal(raw, &store)
	rec := store.Sessions["lo-e2e"]
	lo, _ := rec["local_only"].(map[string]any)
	if lo == nil || lo["agent_id"] != agent || lo["owner"] != "devx-pi-mcp" {
		t.Fatalf("local_only marker: %v", rec)
	}
	if p, _ := rec["ports"].(map[string]any); len(p) != 0 {
		t.Fatalf("ports allocated: %v", rec["ports"])
	}
	if r, ok := rec["routes"]; ok && r != nil && len(r.(map[string]any)) != 0 {
		t.Fatalf("routes recorded: %v", r)
	}
	wt := rec["path"].(string)
	for _, f := range []string{".envrc", ".tmuxp.yaml", ".devx-web-token"} {
		if _, err := os.Stat(filepath.Join(wt, f)); err == nil {
			t.Fatalf("%s present in local-only worktree", f)
		}
	}
	// Only devx-local + pi-agent windows; no editor/services.
	wins, _ := exec.Command("tmux", "list-windows", "-t", "=lo-e2e", "-F", "#{window_name}").Output()
	if got := strings.Fields(string(wins)); strings.Join(got, ",") != "devx-local,pi-agent" {
		t.Fatalf("windows=%v", got)
	}
	// No route config written by the start.
	for _, p := range []string{filepath.Join(cfgDir, "caddy-config.json"), tunnelCfg} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("start wrote route config %s", p)
		}
	}

	// Inspect does not change control or start anything.
	if out, err := devx("agent", "inspect", agent, "--lines", "40"); err != nil || !strings.Contains(out, "HERON") {
		t.Fatalf("inspect: %v\n%s", err, out)
	}
	// Takeover fences sends; release delivers them in the same Pi session.
	if out, err := devx("agent", "takeover", agent); err != nil {
		t.Fatalf("takeover: %v %s", err, out)
	}
	q := mcp("pi_send", map[string]any{"agent_id": agent, "prompt": "held while human", "idempotency_key": "s1"})
	time.Sleep(1500 * time.Millisecond)
	if st := mcp("pi_status", map[string]any{"task_id": q["task_id"]})["task"].(map[string]any); st["state"] != "waiting" || st["waiting_reason"] != "human_control" {
		t.Fatalf("send not fenced: %v", st)
	}
	if out, err := devx("agent", "release", agent); err != nil {
		t.Fatalf("release: %v %s", err, out)
	}
	done := waitState(q["task_id"].(string), 60*time.Second, "completed")
	if !strings.Contains(fmt.Sprint(done["result_excerpt"]), "held while human") {
		t.Fatalf("released result: %v", done)
	}

	// Pi crash -> relaunch: same agent, same Pi session, same local-only
	// tmux session (adopted by tag), no new windows besides the respawn.
	statusJSON, _ := devx("agent", "status", agent, "--json")
	pid := regexp.MustCompile(`"pid": (\d+)`).FindStringSubmatch(statusJSON)
	pane := regexp.MustCompile(`"pane_id": "(%\d+)"`).FindStringSubmatch(statusJSON)
	if pid == nil || pane == nil {
		t.Fatalf("status: %s", statusJSON)
	}
	panePID, err := exec.Command("tmux", "display-message", "-p", "-t", pane[1], "#{pane_pid}").Output()
	if err != nil || !descendsFrom(pid[1], strings.TrimSpace(string(panePID))) {
		t.Fatalf("refusing to signal pid %s (not under fixture pane): %v", pid[1], err)
	}
	if err := exec.Command("kill", "-9", pid[1]).Run(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if js, _ := devx("agent", "status", agent, "--json"); strings.Contains(js, `"bridge_online": false`) {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if out, err := devx("agent", "relaunch", agent); err != nil {
		t.Fatalf("relaunch: %v %s", err, out)
	}
	after := mcp("pi_send", map[string]any{"agent_id": agent, "prompt": "after relaunch", "idempotency_key": "s2"})
	waitState(after["task_id"].(string), 60*time.Second, "completed")
	wins, _ = exec.Command("tmux", "list-windows", "-t", "=lo-e2e", "-F", "#{window_name}").Output()
	if got := strings.Fields(string(wins)); strings.Join(got, ",") != "devx-local,pi-agent" {
		t.Fatalf("windows after relaunch=%v", got)
	}

	// A later default route sync (dangerous config) still excludes it.
	if out, err := devx("cloudflare", "sync"); err != nil {
		t.Fatalf("cloudflare sync: %v %s", err, out)
	}
	if b, _ := os.ReadFile(tunnelCfg); strings.Contains(string(b), "lo-e2e") {
		t.Fatalf("tunnel publishes local-only session:\n%s", b)
	}
	// Hand-added ports must still not be published (marker wins).
	sessPath := filepath.Join(cfgDir, "sessions.json")
	raw, _ = os.ReadFile(sessPath)
	_ = os.WriteFile(sessPath, []byte(strings.Replace(string(raw), `"ports": {}`, `"ports": {"WEB": 41999}`, 1)), 0o600)
	if out, err := devx("cloudflare", "sync"); err != nil {
		t.Fatalf("cloudflare sync 2: %v %s", err, out)
	}
	if b, _ := os.ReadFile(tunnelCfg); strings.Contains(string(b), "lo-e2e") || strings.Contains(string(b), "41999") {
		t.Fatalf("tunnel publishes hand-edited local-only session:\n%s", b)
	}

	// Removing it: owned resources only, no route sync.
	if out, err := devx("session", "rm", "--force", "lo-e2e"); err != nil {
		t.Fatalf("rm: %v %s", err, out)
	}
	if exec.Command("tmux", "has-session", "-t", "=lo-e2e").Run() == nil {
		t.Fatal("tmux session left behind")
	}
	if b, _ := os.ReadFile(trip); len(b) > 0 {
		t.Fatalf("tripwires fired:\n%s", b)
	}
	if after := ownListeners(t); after != listenBefore {
		t.Fatalf("new TCP listeners from test process tree:\nbefore=%s\nafter=%s", listenBefore, after)
	}
	if lp := os.Getenv("DEVX_LOCALONLY_E2E_LOG"); lp != "" {
		_ = os.WriteFile(lp, []byte(fmt.Sprintf("agent=%s session=lo-e2e marker=%v windows=devx-local,pi-agent tripwires=none listeners_unchanged=true\n", agent, lo)), 0o600)
	}
}

// ownListeners lists TCP listeners owned by this test process and its
// descendants (devx, pi, tmux children). Read-only lsof.
func ownListeners(t *testing.T) string {
	t.Helper()
	out, _ := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-Fp").Output()
	me := fmt.Sprint(os.Getpid())
	var mine []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "p") && descendsFrom(l[1:], me) {
			mine = append(mine, l[1:])
		}
	}
	return strings.Join(mine, ",")
}
