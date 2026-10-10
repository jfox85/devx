package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/internal/tmuxfixture"
)

// TestArtifactBridgeReloadsWithoutRestart keeps ONE real `devx mcp pi`
// process alive (as the Agent Shed relay does) and edits the owner config
// between calls: default scope, explicit narrowing, session addition and
// removal, exclusions, allowed-project changes, malformed config and
// recovery all take effect on the next call with no restart.
func TestArtifactBridgeReloadsWithoutRestart(t *testing.T) {
	w := tmuxfixture.ActiveWrapper()
	if w == nil || os.Getenv("HOME") != w.Home {
		t.Skip("isolated test HOME not active")
	}
	home, err := os.MkdirTemp(w.Home, "artifact-reload-e2e-")
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
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	base := "disable_caddy: true\nauto_check_updates: false\nweb_autostart: false\nusage:\n  enabled: false\n"
	writeCfg := func(piMCP string) {
		t.Helper()
		// Atomic replace, the way an editor or the owner's tooling saves.
		tmp := cfgPath + ".tmp"
		if err := os.WriteFile(tmp, []byte(base+piMCP), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, cfgPath); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	sessions := map[string]any{}
	agents := map[string]string{}
	writeSessions := func() {
		b, _ := json.Marshal(map[string]any{"sessions": sessions})
		_ = os.WriteFile(filepath.Join(cfgDir, "sessions.json"), b, 0o600)
	}
	addSession := func(i int, name, project string) {
		wt := filepath.Join(home, "wt", name)
		_ = os.MkdirAll(filepath.Join(wt, ".artifacts"), 0o755)
		id := fmt.Sprintf("pa_00000000000%d", i)
		agents[name] = id
		// MCP-created (local-only) session record naming its agent.
		sessions[name] = map[string]any{"name": name, "project_alias": project, "branch": name, "path": wt, "ports": map[string]int{},
			"local_only": map[string]any{"owner": "devx-pi-mcp", "agent_id": id, "created_at": now}, "created_at": now, "updated_at": now}
		rec := map[string]any{"id": id, "devx_session": name, "project": project, "worktree": wt, "pi_session_id": "x",
			"binding": map[string]any{"tmux_session": name, "window_id": "@1", "pane_id": "%1"},
			"lease":   map[string]any{"holder": "human", "generation": 1, "since": now}, "created_at": now, "updated_at": now}
		b, _ := json.Marshal(rec)
		dir := filepath.Join(cfgDir, "pi-agents", "agents", id)
		_ = os.MkdirAll(dir, 0o700)
		_ = os.WriteFile(filepath.Join(dir, "agent.json"), b, 0o600)
		manifest := fmt.Sprintf(`{"version":1,"session":%q,"artifacts":[{"id":"r1","type":"report","title":"Report %s","file":"r.md","created":%q,"retention":"session"}]}`,
			name, name, now.Format(time.RFC3339Nano))
		_ = os.WriteFile(filepath.Join(wt, ".artifacts", "manifest.json"), []byte(manifest), 0o644)
		_ = os.WriteFile(filepath.Join(wt, ".artifacts", "r.md"), []byte(strings.Repeat("report "+name+"\n", 4000)), 0o644)
		writeSessions()
	}
	addSession(1, "alpha", "synth")
	addSession(2, "beta", "synth")
	addSession(3, "gamma", "other")
	// A legacy MCP session (pre-local-only, or an adoption whose marker an
	// older writer dropped): no marker. Default scope must include it.
	addSession(5, "legacy", "synth")
	delete(sessions["legacy"].(map[string]any), "local_only")
	// A session whose marker names a different agent: never eligible.
	addSession(6, "conflict", "synth")
	sessions["conflict"].(map[string]any)["local_only"] = map[string]any{"owner": "devx-pi-mcp", "agent_id": "pa_999999999999", "created_at": now}
	writeSessions()
	writeCfg("pi_mcp:\n  allowed_projects: [synth]\n") // default scope: read on, upload off

	// One long-lived MCP server process.
	c := exec.Command(bin, "mcp", "pi")
	c.Dir = home
	c.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "DEVX_DISABLE_CADDY=true")
	stdin, _ := c.StdinPipe()
	stdout, _ := c.StdoutPipe()
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	pid := c.Process.Pid
	defer func() { _ = stdin.Close(); _ = c.Wait() }()
	rd := bufio.NewReaderSize(stdout, 1<<20)
	id := 0
	rpc := func(method string, params any) map[string]any {
		t.Helper()
		id++
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if _, err := io.WriteString(stdin, string(b)+"\n"); err != nil {
			t.Fatal(err)
		}
		for {
			line, err := rd.ReadBytes('\n')
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var r map[string]any
			if json.Unmarshal(line, &r) == nil && r["id"] == float64(id) {
				return r["result"].(map[string]any)
			}
		}
	}
	call := func(tool string, args map[string]any) map[string]any {
		return rpc("tools/call", map[string]any{"name": tool, "arguments": args})["structuredContent"].(map[string]any)
	}
	tools := func() []string {
		var out []string
		for _, x := range rpc("tools/list", map[string]any{})["tools"].([]any) {
			if n := x.(map[string]any)["name"].(string); strings.HasPrefix(n, "devx_") {
				out = append(out, n)
			}
		}
		return out
	}
	readable := func(name string) bool {
		sc := call("devx_artifact_list", map[string]any{"agent_id": agents[name]})
		return sc["error"] == nil && len(sc["artifacts"].([]any)) == 1
	}
	expect := func(step string, want map[string]bool) {
		t.Helper()
		for name, ok := range want {
			if got := readable(name); got != ok {
				t.Fatalf("%s: %s readable=%v want %v", step, name, got, ok)
			}
		}
	}
	_ = rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "e2e", "version": "1"}})

	if got := strings.Join(tools(), ","); got != "devx_artifact_list,devx_artifact_read" {
		t.Fatalf("default tools: %s", got)
	}
	expect("default scope", map[string]bool{"alpha": true, "beta": true, "gamma": false, "legacy": true, "conflict": false})

	// Start a chunked read of alpha, then narrow to an explicit list without it.
	list := call("devx_artifact_list", map[string]any{"agent_id": agents["alpha"]})
	art := list["artifacts"].([]any)[0].(map[string]any)
	first := call("devx_artifact_read", map[string]any{"agent_id": agents["alpha"], "artifact_id": art["id"], "version": art["version"], "max_bytes": 1000})
	if first["eof"] == true {
		t.Fatal("expected a multi-chunk artifact")
	}
	writeCfg("pi_mcp:\n  allowed_projects: [synth]\n  artifacts:\n    sessions: [beta]\n")
	if sc := call("devx_artifact_read", map[string]any{"agent_id": agents["alpha"], "artifact_id": art["id"], "version": art["version"], "offset": first["next_offset"]}); sc["error"] != "permission_denied" {
		t.Fatalf("revocation between chunks: %v", sc)
	}
	expect("explicit [beta]", map[string]bool{"alpha": false, "beta": true, "gamma": false})

	// New session created while the server runs, then added to the list.
	addSession(4, "delta", "synth")
	expect("delta not yet listed", map[string]bool{"delta": false})
	writeCfg("pi_mcp:\n  allowed_projects: [synth]\n  artifacts:\n    sessions: [beta, delta]\n")
	expect("delta listed", map[string]bool{"delta": true, "beta": true})

	// Back to default scope with an exclusion; then an allowed-project change.
	writeCfg("pi_mcp:\n  allowed_projects: [synth, other]\n  artifacts:\n    exclude_sessions: [beta]\n")
	expect("default + exclusion + other project", map[string]bool{"alpha": true, "beta": false, "gamma": true, "delta": true})
	writeCfg("pi_mcp:\n  allowed_projects: [other]\n")
	expect("synth removed from allowed_projects", map[string]bool{"alpha": false, "gamma": true})

	// Session removed from DevX while the server runs.
	delete(sessions, "gamma")
	writeSessions()
	expect("gamma session removed", map[string]bool{"gamma": false})

	// Malformed config: tools disappear and every call fails closed.
	writeCfg("pi_mcp:\n  allowed_projects: [synth]\n  artifacts:\n    session: [alpha]\n") // typo key
	if got := tools(); len(got) != 0 {
		t.Fatalf("tools listed with malformed config: %v", got)
	}
	if sc := call("devx_artifact_list", map[string]any{"agent_id": agents["alpha"]}); sc["error"] != "unavailable" {
		t.Fatalf("malformed config list: %v", sc)
	}
	if err := os.WriteFile(cfgPath, []byte("pi_mcp: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sc := call("devx_artifact_list", map[string]any{"agent_id": agents["alpha"]}); sc["error"] != "unavailable" {
		t.Fatalf("unparseable config list: %v", sc)
	}
	// Read disabled explicitly; upload enabled for an explicit session only.
	writeCfg("pi_mcp:\n  allowed_projects: [synth]\n  artifacts:\n    read: false\n    upload: true\n    sessions: [alpha]\n")
	if got := strings.Join(tools(), ","); got != "devx_attachment_upload" {
		t.Fatalf("read off / upload on: %s", got)
	}
	expect("read disabled", map[string]bool{"alpha": false})
	// Recovery to the explicit fixture-style config.
	writeCfg("pi_mcp:\n  allowed_projects: [synth]\n  artifacts:\n    read: true\n    upload: false\n    sessions: [alpha]\n")
	expect("recovered", map[string]bool{"alpha": true, "beta": false, "delta": false})
	if c.Process.Pid != pid || c.ProcessState != nil {
		t.Fatal("server process was restarted")
	}
}
