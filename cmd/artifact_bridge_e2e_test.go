package cmd

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/internal/tmuxfixture"
)

// TestArtifactBridgeBinaryEndToEnd builds the real devx binary and talks to
// `devx mcp pi` over stdio exactly as the Agent Shed relay does (one process
// per call; the relay may also keep one process for many calls). It uses a
// fake HOME with real DevX config, sessions.json, a project registry and a
// managed-agent record, and covers capability gating from config, listing,
// exact image/Markdown readback, chunked upload with idempotent replay, local
// resolution through `devx artifact`, and cross-session denial. No tmux, Pi,
// network or real user state is touched.
func TestArtifactBridgeBinaryEndToEnd(t *testing.T) {
	w := tmuxfixture.ActiveWrapper()
	if w == nil || os.Getenv("HOME") != w.Home {
		t.Skip("isolated test HOME not active")
	}
	home, err := os.MkdirTemp(w.Home, "artifact-bridge-e2e-")
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
	writeCfg := func(read, upload bool) {
		cfg := fmt.Sprintf("disable_caddy: true\nauto_check_updates: false\nweb_autostart: false\nusage:\n  enabled: false\npi_mcp:\n  allowed_projects: [synth]\n  artifacts:\n    read: %v\n    upload: %v\n    max_upload_bytes: 100000\n", read, upload)
		if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(cfg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(), "DEVX_DISABLE_CADDY=true", "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"), "XDG_CACHE_HOME="+filepath.Join(home, ".cache"))

	// Two sessions (worktrees) in the allowed project, each with an agent.
	now := time.Now().UTC()
	sessions := map[string]any{}
	agents := map[string]string{}
	worktrees := map[string]string{}
	for i, name := range []string{"bridge-a", "bridge-b"} {
		wt := filepath.Join(home, "wt", name)
		_ = os.MkdirAll(wt, 0o755)
		id := fmt.Sprintf("pa_00000000000%d", i+1)
		agents[name], worktrees[name] = id, wt
		sessions[name] = map[string]any{"name": name, "project_alias": "synth", "branch": name, "path": wt, "ports": map[string]int{},
			"managed_agent": id, "created_at": now, "updated_at": now}
		rec := map[string]any{"id": id, "devx_session": name, "project": "synth", "worktree": wt, "pi_session_id": "x",
			"binding": map[string]any{"tmux_session": name, "window_id": "@1", "pane_id": "%1"},
			"lease":   map[string]any{"holder": "human", "generation": 1, "since": now}, "created_at": now, "updated_at": now, "adopted": true}
		b, _ := json.Marshal(rec)
		dir := filepath.Join(cfgDir, "pi-agents", "agents", id)
		_ = os.MkdirAll(dir, 0o700)
		_ = os.WriteFile(filepath.Join(dir, "agent.json"), b, 0o600)
	}
	sb, _ := json.Marshal(map[string]any{"sessions": sessions})
	_ = os.WriteFile(filepath.Join(cfgDir, "sessions.json"), sb, 0o600)

	devx := func(dir string, args ...string) (string, error) {
		c := exec.Command(bin, args...)
		c.Dir, c.Env = dir, env
		out, err := c.CombinedOutput()
		return string(out), err
	}
	// Register a synthetic screenshot and Markdown report with the normal CLI.
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 8), 90, 255})
		}
	}
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	shot := pngBuf.Bytes()
	md := []byte("# Synthetic report\n\n" + strings.Repeat("Ünïcödé line ✓ 🚀\n", 2000))
	wtA := worktrees["bridge-a"]
	_ = os.WriteFile(filepath.Join(home, "shot.png"), shot, 0o644)
	_ = os.WriteFile(filepath.Join(home, "report.md"), md, 0o644)
	for _, a := range [][]string{{filepath.Join(home, "shot.png"), "Synthetic screenshot"}, {filepath.Join(home, "report.md"), "Synthetic report"}} {
		if out, err := devx(wtA, "artifact", "add", a[0], "--title", a[1], "--session", "bridge-a"); err != nil {
			t.Fatalf("artifact add: %v %s", err, out)
		}
	}

	n := 0
	mcp := func(tool string, args map[string]any) (map[string]any, map[string]any) {
		t.Helper()
		n++
		a, _ := json.Marshal(args)
		in := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}
{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}
`, n+1, tool, a)
		c := exec.Command(bin, "mcp", "pi")
		c.Dir, c.Env, c.Stdin = home, env, strings.NewReader(in)
		out, err := c.Output()
		if err != nil {
			t.Fatalf("mcp %s: %v", tool, err)
		}
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(make([]byte, 1<<20), 1<<22)
		for sc.Scan() {
			var r map[string]any
			if json.Unmarshal(sc.Bytes(), &r) == nil && r["id"] == float64(n+1) {
				if len(sc.Bytes()) > 64<<10 {
					t.Fatalf("%s response %d bytes exceeds the gateway's 64 KiB", tool, len(sc.Bytes()))
				}
				res := r["result"].(map[string]any)
				return res["structuredContent"].(map[string]any), res
			}
		}
		t.Fatalf("no response for %s: %s", tool, out)
		return nil, nil
	}
	toolNames := func() map[string]bool {
		c := exec.Command(bin, "mcp", "pi")
		c.Env, c.Stdin = env, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n")
		out, err := c.Output()
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			Result struct {
				Tools []struct{ Name string } `json:"tools"`
			} `json:"result"`
		}
		_ = json.Unmarshal(bytes.TrimSpace(out), &r)
		m := map[string]bool{}
		for _, tl := range r.Result.Tools {
			m[tl.Name] = true
		}
		return m
	}
	agentA, agentB := agents["bridge-a"], agents["bridge-b"]

	// Default config: no artifact tools at all.
	writeCfg(false, false)
	if names := toolNames(); names["devx_artifact_list"] || names["devx_attachment_upload"] || len(names) != 7 {
		t.Fatalf("capabilities off: %v", names)
	}
	if sc, _ := mcp("devx_artifact_list", map[string]any{"agent_id": agentA}); sc["error"] != "permission_denied" {
		t.Fatalf("disabled list: %v", sc)
	}
	// Read only.
	writeCfg(true, false)
	if names := toolNames(); !names["devx_artifact_read"] || names["devx_attachment_upload"] {
		t.Fatalf("read-only: %v", names)
	}
	list, _ := mcp("devx_artifact_list", map[string]any{"agent_id": agentA})
	arts := list["artifacts"].([]any)
	if len(arts) != 2 {
		t.Fatalf("list: %v", list)
	}
	byTitle := map[string]map[string]any{}
	for _, x := range arts {
		byTitle[x.(map[string]any)["title"].(string)] = x.(map[string]any)
	}
	si, ri := byTitle["Synthetic screenshot"], byTitle["Synthetic report"]
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	if si["checksums"].(map[string]any)["sha256"] != sum(shot) || ri["checksums"].(map[string]any)["sha256"] != sum(md) || si["mime_type"] != "image/png" {
		t.Fatalf("checksums/mime: %v %v", si, ri)
	}
	// Image comes back as an MCP image block with exact bytes.
	_, res := mcp("devx_artifact_read", map[string]any{"agent_id": agentA, "artifact_id": si["id"], "version": si["version"]})
	blk := res["content"].([]any)[0].(map[string]any)
	got, _ := base64.StdEncoding.DecodeString(blk["data"].(string))
	if blk["type"] != "image" || !bytes.Equal(got, shot) {
		t.Fatal("screenshot bytes differ")
	}
	// Markdown in chunks; reassembled bytes equal the original.
	var all []byte
	for off := float64(0); ; {
		sc, _ := mcp("devx_artifact_read", map[string]any{"agent_id": agentA, "artifact_id": ri["id"], "version": ri["version"], "offset": off})
		if sc["error"] != nil {
			t.Fatalf("read: %v", sc)
		}
		all = append(all, []byte(sc["text"].(string))...)
		off = sc["next_offset"].(float64)
		if sc["eof"] == true {
			break
		}
	}
	if !bytes.Equal(all, md) {
		t.Fatal("markdown reassembly differs")
	}
	// Cross-session: agent B cannot read agent A's artifact id.
	if sc, _ := mcp("devx_artifact_read", map[string]any{"agent_id": agentB, "artifact_id": si["id"]}); sc["error"] != "not_found" {
		t.Fatalf("cross-session: %v", sc)
	}
	// Upload denied while only read is enabled.
	ref := []byte(strings.Repeat("reference ", 2000)) // 20 KB -> 3 chunks
	up := func(off int, data bool) map[string]any {
		args := map[string]any{"agent_id": agentA, "idempotency_key": "e2e-ref", "filename": "brief.txt", "mime_type": "text/plain",
			"size": len(ref), "sha256": sum(ref), "offset": off}
		if data {
			end := off + 8192
			if end > len(ref) {
				end = len(ref)
			}
			args["data_base64"] = base64.StdEncoding.EncodeToString(ref[off:end])
		}
		sc, _ := mcp("devx_attachment_upload", args)
		return sc
	}
	if sc := up(0, true); sc["error"] != "permission_denied" {
		t.Fatalf("upload with read-only: %v", sc)
	}
	// Enable upload; send first chunk, "lose" the connection, probe, resume.
	writeCfg(true, true)
	if sc := up(0, true); sc["next_offset"] != float64(8192) {
		t.Fatalf("chunk 1: %v", sc)
	}
	if sc := up(0, false); sc["next_offset"] != float64(8192) {
		t.Fatalf("probe: %v", sc)
	}
	_ = up(8192, true)
	done := up(16384, true)
	if done["state"] != "complete" {
		t.Fatalf("upload: %v", done)
	}
	if again := up(16384, true); again["replayed"] != true {
		t.Fatalf("replay: %v", again)
	}
	local := done["local"].(map[string]any)
	// The local Pi agent resolves the attachment through the normal CLI.
	out, err := devx(wtA, "artifact", "url", local["manifest_id"].(string), "--local", "--session", "bridge-a")
	if err != nil {
		t.Fatalf("artifact url: %v %s", err, out)
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(wtA, p)
	}
	b, err := os.ReadFile(p)
	if err != nil || !bytes.Equal(b, ref) {
		t.Fatalf("local readback at %s: %v", p, err)
	}
	if !strings.HasPrefix(local["path"].(string), ".artifacts/attachments/") {
		t.Fatalf("attachment path: %v", local)
	}
	// Agent B sees nothing of A's.
	lb, _ := mcp("devx_artifact_list", map[string]any{"agent_id": agentB})
	if len(lb["artifacts"].([]any)) != 0 {
		t.Fatalf("agent B list leaked: %v", lb)
	}
	// Project removed from the allowlist: everything denied on the next call.
	_ = os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte("pi_mcp:\n  allowed_projects: [other]\n  artifacts:\n    read: true\n    upload: true\n"), 0o600)
	if sc, _ := mcp("devx_artifact_list", map[string]any{"agent_id": agentA}); sc["error"] != "permission_denied" {
		t.Fatalf("de-allowlisted project: %v", sc)
	}
	// The worktree's tracked area was not written: only .artifacts changed.
	entries, _ := os.ReadDir(wtA)
	for _, e := range entries {
		if e.Name() != ".artifacts" {
			t.Fatalf("unexpected file in worktree: %s", e.Name())
		}
	}
}
