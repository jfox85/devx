package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/internal/tmuxfixture"
)

// TestLaunchPolicyIgnoresProjectConfig drives the real devx binary
// (`devx mcp pi` over stdio) to prove the managed-agent launch policy
// (pi_mcp.allowed_projects, pi_command, pi_args, pass_env) comes only from
// the owner's config file or an explicit --config, never from a project
// .devx/config.yaml, regardless of the working directory, and never from
// DEVX_* environment variables. A project .devx/config.yaml still applies to
// unrelated keys.
//
// Everything is synthetic: fake HOME, owned tmux socket, and a recorder
// script in place of Pi. The "repository" executable is a tripwire that is
// never expected to run.
func TestLaunchPolicyIgnoresProjectConfig(t *testing.T) {
	if os.Getenv("DEVX_CLI_E2E") == "0" {
		t.Skip("DEVX_CLI_E2E=0")
	}
	w := tmuxfixture.ActiveWrapper()
	if w == nil || w.RealTmux == "" || os.Getenv("HOME") != w.Home {
		t.Skip("owned tmux wrapper / fake HOME not active")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home, err := os.MkdirTemp(w.Home, "lp-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, _ := filepath.Abs("..")
	binDir := filepath.Join(home, "bin")
	_ = os.MkdirAll(binDir, 0o700)
	bin := filepath.Join(binDir, "devx")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build devx: %v\n%s", err, out)
	}

	// Owner "Pi": records its argv and two env vars, then idles.
	record := filepath.Join(home, "owner-pi.log")
	ownerPi := filepath.Join(binDir, "owner-pi")
	_ = os.WriteFile(ownerPi, []byte(fmt.Sprintf("#!/bin/sh\n{ echo \"argv $*\"; echo \"owner=${SYNTH_OWNER_VAR-unset} repo=${SYNTH_REPO_VAR-unset}\"; } >> %s\nexec sleep 300\n", record)), 0o700)
	// Repository-supplied "executable": must never run.
	trip := filepath.Join(home, "tripwire.log")
	repoPi := filepath.Join(binDir, "repo-pi")
	_ = os.WriteFile(repoPi, []byte(fmt.Sprintf("#!/bin/sh\necho \"repo-pi $*\" >> %s\nexit 97\n", trip)), 0o700)

	// Synthetic project whose .devx/config.yaml tries to take
	// over the launch policy, plus an unrelated key that must still apply.
	repo := filepath.Join(home, "repo")
	mustRun(t, "", "git", "init", "-q", "-b", "main", repo)
	_ = os.MkdirAll(filepath.Join(repo, ".devx"), 0o755)
	_ = os.MkdirAll(filepath.Join(repo, "sub"), 0o755)
	projCfg := fmt.Sprintf("editor: synth-project-editor\npi_mcp:\n  allowed_projects: [repo-proj, synth]\n  pi_command: %s\n  pi_args: [--repo-arg]\n  pass_env: [SYNTH_REPO_VAR]\n", repoPi)
	_ = os.WriteFile(filepath.Join(repo, ".devx", "config.yaml"), []byte(projCfg), 0o644)
	mustRun(t, repo, "git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")

	cfgDir := filepath.Join(home, ".config", "devx")
	_ = os.MkdirAll(cfgDir, 0o700)
	ownerCfgPath := filepath.Join(cfgDir, "config.yaml")
	writeOwner := func(body string, mode os.FileMode) {
		t.Helper()
		base := "disable_caddy: true\ntarget: host\nports: []\nauto_check_updates: false\nweb_autostart: false\nusage:\n  enabled: false\neditor: synth-owner-editor\n"
		if err := os.WriteFile(ownerCfgPath, []byte(base+body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(ownerCfgPath, mode); err != nil {
			t.Fatal(err)
		}
	}
	ownerPolicy := fmt.Sprintf("pi_mcp:\n  allowed_projects: [synth]\n  pi_command: %s\n  pi_args: [--owner-arg]\n  pass_env: [SYNTH_OWNER_VAR]\n", ownerPi)
	writeOwner(ownerPolicy, 0o600)

	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"DEVX_DISABLE_CADDY=true",
		// DEVX_* env must not affect the launch policy either.
		"DEVX_PI_MCP.ALLOWED_PROJECTS=env-proj", "DEVX_PI_MCP_ALLOWED_PROJECTS=env-proj")
	// The pass_env candidates exist only in the `devx mcp pi` process, so a
	// value reaching Pi can only come through the launch script's exports,
	// not the tmux server's environment. Start the owned tmux server (or
	// reuse it) from a process without them.
	mcpEnv := append(append([]string{}, env...), "SYNTH_OWNER_VAR=owner-value", "SYNTH_REPO_VAR=repo-value")
	keep := exec.Command("tmux", "new-session", "-d", "-s", "lp-e2e-keepalive", "sleep", "600")
	keep.Env = env
	if out, err := keep.CombinedOutput(); err != nil {
		t.Fatalf("tmux keepalive: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "=lp-e2e-keepalive").Run() })
	devxIn := func(dir string, args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir = dir
		c.Env = env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	if out, err := devxIn(home, "project", "add", repo, "--alias", "synth"); err != nil {
		t.Fatalf("project add: %v\n%s", err, out)
	}
	// Unrelated project keys still apply inside the project.
	if out, _ := devxIn(filepath.Join(repo, "sub"), "config", "get", "editor"); strings.TrimSpace(out) != "synth-project-editor" {
		t.Fatalf("project config no longer applies to unrelated keys: %q", out)
	}
	if out, _ := devxIn(home, "config", "get", "editor"); strings.TrimSpace(out) != "synth-owner-editor" {
		t.Fatalf("owner editor outside project: %q", out)
	}

	n := 0
	mcp := func(dir string, extra []string, tool string, args map[string]any) map[string]any {
		t.Helper()
		n++
		a, _ := json.Marshal(args)
		in := fmt.Sprintf("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-06-18\",\"capabilities\":{},\"clientInfo\":{\"name\":\"e2e\",\"version\":\"1\"}}}\n{\"jsonrpc\":\"2.0\",\"id\":%d,\"method\":\"tools/call\",\"params\":{\"name\":%q,\"arguments\":%s}}\n", n+1, tool, a)
		c := exec.Command(bin, append(extra, "mcp", "pi")...)
		c.Dir = dir
		c.Env = mcpEnv
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
	allowed := func(dir string, extra ...string) string {
		t.Helper()
		res := mcp(dir, extra, "pi_list_sessions", map[string]any{})
		if res["_isError"] == true {
			t.Fatalf("pi_list_sessions must keep working: %v", res)
		}
		if res["allowed_projects"] == nil {
			return "[]"
		}
		return fmt.Sprint(res["allowed_projects"])
	}
	inside := filepath.Join(repo, "sub")

	// 1. Allowlist: owner's, whether cwd is outside or inside the project.
	for _, dir := range []string{home, repo, inside} {
		if got := allowed(dir); got != "[synth]" {
			t.Fatalf("cwd %s: allowed_projects=%s, want owner's [synth]", dir, got)
		}
	}
	// The project's extra entry cannot be started.
	if r := mcp(inside, nil, "pi_start_task", map[string]any{"project": "repo-proj", "session_name": "lp-denied", "prompt": "x", "idempotency_key": "lp-d"}); r["_isError"] != true || !strings.Contains(fmt.Sprint(r), "allowlist") {
		t.Fatalf("project-only allowlist entry must be denied: %v", r)
	}

	// 2. Start from inside the project: owner command, args and pass_env.
	r := mcp(inside, nil, "pi_start_task", map[string]any{"project": "synth", "session_name": "lp-e2e", "prompt": "hi", "idempotency_key": "lp-1"})
	if r["_isError"] == true {
		t.Fatalf("start: %v", r)
	}
	agent := r["agent_id"].(string)
	t.Cleanup(func() { _, _ = devxIn(home, "session", "rm", "--force", "lp-e2e") })
	script, err := os.ReadFile(filepath.Join(cfgDir, "pi-agents", "agents", agent, "launch.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(script)
	for _, want := range []string{shellQ(ownerPi), "'--owner-arg'", "export SYNTH_OWNER_VAR='owner-value'"} {
		if !strings.Contains(s, want) {
			t.Fatalf("launch script lacks owner policy %q:\n%s", want, s)
		}
	}
	for _, bad := range []string{repoPi, "--repo-arg", "SYNTH_REPO_VAR", "repo-value"} {
		if strings.Contains(s, bad) {
			t.Fatalf("launch script contains project policy %q:\n%s", bad, s)
		}
	}
	// The subprocess actually launched is the owner's, with owner args/env.
	var rec string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if b, _ := os.ReadFile(record); strings.Contains(string(b), "owner=") {
			rec = string(b)
			break
		}
	}
	if !strings.Contains(rec, "--owner-arg") || strings.Contains(rec, "--repo-arg") || !strings.Contains(rec, "owner=owner-value repo=unset") {
		t.Fatalf("owner pi record: %q", rec)
	}

	// 3. An explicit --config is respected, from inside the project too.
	explicit := filepath.Join(home, "explicit.yaml")
	_ = os.WriteFile(explicit, []byte("pi_mcp:\n  allowed_projects: [explicit-proj]\n"), 0o600)
	if got := allowed(inside, "--config", explicit); got != "[explicit-proj]" {
		t.Fatalf("--config: allowed_projects=%s", got)
	}
	// An explicit --config is used as the owner file: a group-writable one
	// is refused like the default.
	_ = os.Chmod(explicit, 0o620)
	if got := allowed(inside, "--config", explicit); got != "[]" {
		t.Fatalf("untrusted --config: allowed_projects=%s", got)
	}

	// 4. Fail closed: an untrusted or invalid owner file never falls back
	// to the project config; starts and relaunches are refused, reads work.
	// (A valid file with no pi_mcp block is not an error: it is the empty
	// policy, which denies every start. Relaunch of an existing agent was
	// never allowlist-gated, so only unusable files are checked for it.)
	for _, c := range []struct {
		name     string
		body     string
		mode     os.FileMode
		unusable bool
	}{
		{"group-writable", ownerPolicy, 0o620, true},
		{"invalid", "pi_mcp:\n  allowed_projects: synth\n", 0o600, true},
		{"no-policy", "", 0o600, false},
	} {
		writeOwner(c.body, c.mode)
		if got := allowed(inside); got != "[]" {
			t.Fatalf("%s: allowed_projects=%s, want []", c.name, got)
		}
		res := mcp(inside, nil, "pi_start_task", map[string]any{"project": "synth", "session_name": "lp-" + c.name, "prompt": "x", "idempotency_key": "lp-" + c.name})
		if res["_isError"] != true {
			t.Fatalf("%s: start must be denied: %v", c.name, res)
		}
		// The MCP client never sees the owner config path.
		if strings.Contains(fmt.Sprint(res), cfgDir) {
			t.Fatalf("%s: MCP denial leaks the config path: %v", c.name, res)
		}
		if c.unusable {
			out, err := devxIn(inside, "agent", "relaunch", agent, "--force")
			if err == nil || !strings.Contains(out, "launch policy unusable, starts and relaunches are refused: ") {
				t.Fatalf("%s: relaunch must fail closed: %v %s", c.name, err, out)
			}
		}
		if out, err := devxIn(inside, "agent", "status", agent); err != nil {
			t.Fatalf("%s: status must keep working: %v %s", c.name, err, out)
		}
	}
	writeOwner(ownerPolicy, 0o600)

	if b, _ := os.ReadFile(trip); len(b) > 0 {
		t.Fatalf("repository executable ran:\n%s", b)
	}
}

func shellQ(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }
