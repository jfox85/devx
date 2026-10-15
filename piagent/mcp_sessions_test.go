package piagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func sessionInventoryFixture(t *testing.T, count, width int) *MCPServer {
	t.Helper()
	m, _ := newTestManager(t)
	for i := 0; i < count; i++ {
		id := newAgentID()
		if err := m.Store.ensureAgentDirs(id); err != nil {
			t.Fatal(err)
		}
		// Every record is retired, so fixture reconciliation cannot launch or
		// dispatch anything. Escapes and non-ASCII exercise encoded-byte accounting.
		now := m.Store.now()
		a := &Agent{ID: id, DevxSession: fmt.Sprintf("fixture-%03d-", i) + strings.Repeat("🙂\"\\\n", width), PiSessionID: "fixture", RetiredAt: &now}
		if err := m.Store.SaveAgent(a); err != nil {
			t.Fatal(err)
		}
	}
	return &MCPServer{M: m}
}

func sessionPage(t *testing.T, s *MCPServer, args any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(map[string]any{"name": "pi_list_sessions", "arguments": json.RawMessage(raw)})
	result, rpcErr := s.handle(rpcRequest{Method: "tools/call", Params: params})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	r := result.(map[string]any)
	if r["isError"] != false {
		t.Fatalf("page failed: %v", r)
	}
	encoded, err := json.Marshal(r)
	if err != nil || len(encoded) > MaxSessionListBytes || !utf8.Valid(encoded) {
		t.Fatalf("invalid bounded result: bytes=%d err=%v", len(encoded), err)
	}
	// Numeric, string and long escaped IDs retain room for JSON-RPC framing.
	for _, id := range []json.RawMessage{json.RawMessage(`0`), json.RawMessage(`"same-id"`), json.RawMessage(`"` + strings.Repeat(`\u1234`, 512) + `"`)} {
		framed, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: r})
		if err != nil || len(framed) > 65536 {
			t.Fatalf("oversize RPC frame: %d %v", len(framed), err)
		}
	}
	data := r["structuredContent"].(map[string]any)
	text := r["content"].([]map[string]any)[0]["text"].(string)
	wantText, _ := json.Marshal(data)
	if text != string(wantText) {
		t.Fatal("text-only clients lost structured inventory")
	}
	return data
}

func TestSessionInventoryPaginationCompleteAndBounded(t *testing.T) {
	s := sessionInventoryFixture(t, 45, 200)
	s.M.Config.AllowedProjects = nil
	for i := 0; i < 130; i++ {
		s.M.Config.AllowedProjects = append(s.M.Config.AllowedProjects, fmt.Sprintf("project-%03d-", i)+strings.Repeat("\"🙂", 150))
	}
	agents, projects := map[string]bool{}, map[string]bool{}
	cursor := ""
	pages, byteLimited := 0, false
	for {
		page := sessionPage(t, s, map[string]any{"limit": 100, "cursor": cursor})
		rows := page["agents"].([]map[string]any)
		allowed := page["allowed_projects"].([]string)
		if len(rows)+len(allowed) < 100 && page["eof"] == false {
			byteLimited = true
		}
		for _, a := range rows {
			id := a["agent_id"].(string)
			if agents[id] {
				t.Fatal("duplicate agent")
			}
			agents[id] = true
			if a["state"] != AgentRetired {
				t.Fatalf("retired record inaccessible: %v", a["state"])
			}
		}
		for _, p := range allowed {
			if projects[p] {
				t.Fatal("duplicate project")
			}
			projects[p] = true
		}
		pages++
		if page["eof"] == true {
			if page["next_cursor"] != "" {
				t.Fatal("EOF cursor")
			}
			break
		}
		cursor = page["next_cursor"].(string)
		if cursor == "" || pages > 100 {
			t.Fatal("pagination failed to progress")
		}
	}
	if len(agents) != 45 || len(projects) != 130 || !byteLimited {
		t.Fatalf("incomplete inventory agents=%d projects=%d byteLimited=%v", len(agents), len(projects), byteLimited)
	}
}

func TestSessionInventoryCursorsAndDefaults(t *testing.T) {
	s := sessionInventoryFixture(t, 25, 0)
	first := sessionPage(t, s, map[string]any{})
	if len(first["agents"].([]map[string]any)) != 20 {
		t.Fatal("default limit")
	}
	cursor := first["next_cursor"].(string)
	second := sessionPage(t, s, map[string]any{"cursor": cursor})
	if len(second["agents"].([]map[string]any)) != 5 || len(second["allowed_projects"].([]string)) != 1 || second["eof"] != true {
		t.Fatal("last page")
	}
	// Live status/retirement changes do not invalidate membership cursors.
	as, _ := s.M.Store.ListAgents()
	as[0].Lease.Reason = "changed"
	if err := s.M.Store.SaveAgent(as[0]); err != nil {
		t.Fatal(err)
	}
	sessionPage(t, s, map[string]any{"cursor": cursor})
	for _, args := range []string{`{"limit":0}`, `{"limit":101}`, `{"limit":1.5}`, `{"unknown":true}`, `{"cursor":"invalid"}`, `{"cursor":"` + encodeSessionCursor("wrong", 20) + `"}`, `{"cursor":"` + encodeSessionCursor("wrong", -1) + `"}`} {
		if _, err := s.listSessions(json.RawMessage(args)); err == nil {
			t.Fatalf("accepted %s", args)
		}
	}
	s.M.Config.AllowedProjects = append(s.M.Config.AllowedProjects, "new-project")
	raw, _ := json.Marshal(map[string]any{"cursor": cursor})
	if _, err := s.listSessions(raw); err == nil || !strings.Contains(err.Error(), "inventory changed") {
		t.Fatalf("stale cursor accepted: %v", err)
	}
}

func TestSessionInventoryEmptyAndOversizedEntry(t *testing.T) {
	s := sessionInventoryFixture(t, 0, 0)
	s.M.Config.AllowedProjects = nil
	p := sessionPage(t, s, map[string]any{})
	if p["eof"] != true || len(p["agents"].([]map[string]any)) != 0 || len(p["allowed_projects"].([]string)) != 0 {
		t.Fatal("empty page")
	}
	s.M.Config.AllowedProjects = []string{strings.Repeat("🙂\"", 10000)}
	if _, err := s.listSessions(nil); err == nil || !strings.Contains(err.Error(), "no entries skipped") {
		t.Fatalf("oversized project silently dropped: %v", err)
	}
	s = sessionInventoryFixture(t, 1, 10000)
	if _, err := s.listSessions(nil); err == nil || !strings.Contains(err.Error(), "no entries skipped") {
		t.Fatalf("oversized agent silently dropped: %v", err)
	}
}

func TestSessionInventoryEncodedByteBoundary(t *testing.T) {
	s := sessionInventoryFixture(t, 0, 0)
	s.M.Config.AllowedProjects = []string{""}
	p, err := s.listSessions(nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(sessionListResult(p))
	// Plain ASCII appears once in each compatibility representation.
	width := (MaxSessionListBytes - len(encoded)) / 2
	s.M.Config.AllowedProjects = []string{strings.Repeat("x", width)}
	page := sessionPage(t, s, map[string]any{})
	atBoundary, _ := json.Marshal(sessionListResult(page))
	if len(atBoundary) < MaxSessionListBytes-1 {
		t.Fatalf("fixture missed boundary: %d", len(atBoundary))
	}
	s.M.Config.AllowedProjects[0] += "x"
	if _, err := s.listSessions(nil); err == nil {
		t.Fatal("accepted result over byte budget")
	}
}

func TestSessionInventoryAgentMembershipAndPageReconciliation(t *testing.T) {
	s := sessionInventoryFixture(t, 4, 0)
	agents, _ := s.M.Store.ListAgents()
	for _, a := range agents {
		a.Binding.PaneID = "%1"
		if err := s.M.Store.SaveAgent(a); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	s.M.Tmux = Tmux{Exec: func(args ...string) (string, error) { calls++; return "", fmt.Errorf("fixture offline") }}
	p := sessionPage(t, s, map[string]any{"limit": 1})
	if calls != 1 {
		t.Fatalf("reconciled off-page records: tmux calls=%d", calls)
	}
	raw, _ := json.Marshal(map[string]any{"cursor": p["next_cursor"]})
	copyAgent := *agents[0]
	copyAgent.ID = newAgentID()
	if err := s.M.Store.ensureAgentDirs(copyAgent.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.M.Store.SaveAgent(&copyAgent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.listSessions(raw); err == nil {
		t.Fatal("agent addition did not invalidate cursor")
	}
	if err := os.RemoveAll(filepath.Dir(s.M.Store.agentPath(copyAgent.ID))); err != nil {
		t.Fatal(err)
	}
	sessionPage(t, s, map[string]any{"cursor": p["next_cursor"]})
	if err := os.RemoveAll(filepath.Dir(s.M.Store.agentPath(agents[1].ID))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.listSessions(raw); err == nil {
		t.Fatal("agent removal did not invalidate cursor")
	}
}

func TestSessionInventoryErrorsAreBounded(t *testing.T) {
	s := sessionInventoryFixture(t, 0, 0)
	params, _ := json.Marshal(map[string]any{"name": "pi_list_sessions", "arguments": map[string]any{strings.Repeat("\\\"", 40000): true}})
	r, rpcErr := s.handle(rpcRequest{Method: "tools/call", Params: params})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	result := r.(map[string]any)
	encoded, err := json.Marshal(result)
	if err != nil || result["isError"] != true || len(encoded) > MaxSessionListBytes {
		t.Fatalf("unbounded error %d %v", len(encoded), err)
	}
}
