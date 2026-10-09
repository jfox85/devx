package artifactbridge

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/piagent"
)

// rpc drives the real piagent MCP server over its stdio framing with the
// bridge attached, the same way the Agent Shed relay's stdio client does.
func rpc(t *testing.T, s *piagent.MCPServer, reqs ...string) map[float64]map[string]any {
	t.Helper()
	pr, pw := io.Pipe()
	var out bytes.Buffer
	done := make(chan error)
	go func() { done <- s.Serve(pr, &out) }()
	for _, r := range reqs {
		_, _ = pw.Write([]byte(r + "\n"))
	}
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	res := map[float64]map[string]any{}
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		if len(sc.Bytes()) > 64<<10 {
			t.Fatalf("response line is %d bytes (> 64 KiB)", len(sc.Bytes()))
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if id, ok := m["id"].(float64); ok {
			res[id] = m
		}
	}
	return res
}

func call(id int, name string, args any) string {
	b, _ := json.Marshal(args)
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, name, b)
}

func structured(t *testing.T, r map[string]any) map[string]any {
	t.Helper()
	res := r["result"].(map[string]any)
	return res["structuredContent"].(map[string]any)
}

func TestMCPServerWithBridge(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	shot := syntheticPNG(t, 32, 32)
	md := "# Report\n\nSynthetic ✓\n"
	e.register(s, "Screenshot", "shot.png", shot)
	e.register(s, "Report", "report.md", []byte(md))
	m := piagent.NewManager(e.store, piagent.Tmux{Exec: func(...string) (string, error) { return "", fmt.Errorf("no tmux") }}, nil,
		piagent.Config{AllowedProjects: []string{"proj"}})
	srv := &piagent.MCPServer{M: m, Name: "devx-pi", Version: "test", Extra: e.svc}

	ref := []byte("reference notes\n")
	out := rpc(t, srv,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		call(3, ToolList, map[string]any{"agent_id": a.ID}),
		call(4, ToolUpload, map[string]any{"agent_id": a.ID, "idempotency_key": "u1", "filename": "notes.txt", "mime_type": "text/plain",
			"size": len(ref), "sha256": shaHex(ref), "offset": 0, "data_base64": base64.StdEncoding.EncodeToString(ref)}),
		call(5, ToolList, map[string]any{"agent_id": a.ID, "extra": 1}),
		call(6, "devx_shell", map[string]any{}),
	)
	if !strings.Contains(out[1]["result"].(map[string]any)["instructions"].(string), ToolList) {
		t.Fatal("initialize instructions should mention the artifact tools")
	}
	names := map[string]bool{}
	for _, tl := range out[2]["result"].(map[string]any)["tools"].([]any) {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	if len(names) != 10 || !names[ToolList] || !names[ToolRead] || !names[ToolUpload] || !names["pi_send"] {
		t.Fatalf("tools: %v", names)
	}
	list := structured(t, out[3])
	arts := list["artifacts"].([]any)
	if len(arts) != 2 {
		t.Fatalf("list: %v", list)
	}
	up := structured(t, out[4])
	if up["state"] != "complete" {
		t.Fatalf("upload: %v", up)
	}
	if out[5]["result"].(map[string]any)["isError"] != true {
		t.Fatal("unknown argument must be rejected")
	}
	if structured(t, out[6])["error"] != "permission_denied" {
		t.Fatal("unknown tool must be denied")
	}

	// Read each listed artifact back over MCP and compare exact bytes.
	var reqs []string
	for i, x := range arts {
		it := x.(map[string]any)
		reqs = append(reqs, call(10+i, ToolRead, map[string]any{"agent_id": a.ID, "artifact_id": it["id"], "version": it["version"]}))
	}
	back := rpc(t, srv, reqs...)
	for i, x := range arts {
		it := x.(map[string]any)
		res := back[float64(10+i)]["result"].(map[string]any)
		block := res["content"].([]any)[0].(map[string]any)
		switch it["title"] {
		case "Screenshot":
			data, _ := base64.StdEncoding.DecodeString(block["data"].(string))
			if block["type"] != "image" || !bytes.Equal(data, shot) {
				t.Fatal("screenshot bytes differ over MCP")
			}
		case "Report":
			if block["type"] != "text" || block["text"] != md || res["structuredContent"].(map[string]any)["text"] != md {
				t.Fatal("markdown differs over MCP")
			}
		}
	}

	// Without the provider, the server is unchanged: 7 tools, artifact tools unknown.
	plain := &piagent.MCPServer{M: m, Name: "devx-pi", Version: "test"}
	o := rpc(t, plain, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, call(2, ToolList, map[string]any{"agent_id": a.ID}))
	if n := len(o[1]["result"].(map[string]any)["tools"].([]any)); n != 7 {
		t.Fatalf("plain server tool count %d", n)
	}
	if structured(t, o[2])["error"] != "permission_denied" {
		t.Fatal("artifact tool must be unknown without the provider")
	}
}

func TestPiStatusIncludesProducedArtifacts(t *testing.T) {
	e := newEnv(t)
	a, s := e.agent("s1", "proj")
	m := piagent.NewManager(e.store, piagent.Tmux{Exec: func(...string) (string, error) { return "", fmt.Errorf("no tmux") }}, nil,
		piagent.Config{AllowedProjects: []string{"proj"}})
	r, err := m.Send(piagent.SendRequest{AgentID: a.ID, Prompt: "make a report", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the bridge delivering and finishing the task.
	delivered := time.Now().UTC()
	task, err := e.store.LoadTask(a.ID, r.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	task.State, task.DeliveredAt = piagent.TaskRunning, &delivered
	if err := e.store.WithAgentLock(a.ID, func() error { return e.store.SaveTask(task) }); err != nil {
		t.Fatal(err)
	}
	e.now = delivered.Add(time.Second)
	e.register(s, "Produced", "out.md", []byte("done"))
	srv := &piagent.MCPServer{M: m, Name: "devx-pi", Version: "test", Extra: e.svc}
	out := rpc(t, srv, call(1, "pi_status", map[string]any{"task_id": r.TaskID}))
	st := structured(t, out[1])
	arts, ok := st["artifacts"].([]any)
	if !ok || len(arts) != 1 || arts[0].(map[string]any)["title"] != "Produced" || !strings.HasPrefix(arts[0].(map[string]any)["id"].(string), "dxa_") {
		t.Fatalf("pi_status artifacts: %v", st["artifacts"])
	}
	// Read disabled: pi_status carries no artifacts key (compatible shape).
	e.svc.Cfg.Read = false
	st = structured(t, rpc(t, srv, call(1, "pi_status", map[string]any{"task_id": r.TaskID}))[1])
	if _, present := st["artifacts"]; present {
		t.Fatal("artifacts must be omitted when reading is disabled")
	}
}
