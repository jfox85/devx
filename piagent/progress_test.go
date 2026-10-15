package piagent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const progressSecret = "sk-ant-api03-ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"

// runningTaskWithBridge returns an accepted running task held by a live
// bridge speaking proto.
func runningTaskWithBridge(t *testing.T, m *Manager, proto int) (*Agent, *Task) {
	t.Helper()
	a := seedAgent(t, m)
	liveBridge(t, m, a.ID, proto)
	tk := sendT(t, m, a.ID, "long task", "k")
	tk = bridgeWrite(t, m, a.ID, tk.ID, func(x *Task) { claim(x); accept(x) })
	return a, tk
}

func writeProgress(t *testing.T, m *Manager, agentID string, p Progress) {
	t.Helper()
	if err := writeJSONAtomic(m.Store.progressPath(agentID, p.TaskID), p); err != nil {
		t.Fatal(err)
	}
}

func mcpCall(t *testing.T, s *MCPServer, tool string, args map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	res := byID(mcpRoundTrip(t, s, string(b)), 1)["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("%s failed: %+v", tool, res["structuredContent"])
	}
	return res["structuredContent"].(map[string]any)
}

func TestLatestProgressInStatusRedactedAndBounded(t *testing.T) {
	m, _ := newTestManager(t)
	a, tk := runningTaskWithBridge(t, m, BridgeProtocol)
	at := time.Now().UTC().Truncate(time.Millisecond)
	long := "Found the key " + progressSecret + " in .env; " + strings.Repeat("é and more words ", 60)
	writeProgress(t, m, a.ID, Progress{TaskID: tk.ID, Turn: 7, At: at, Excerpt: long, LastTool: "bash"})

	tv, av, err := m.TaskStatus(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := tv.LatestProgress
	if p == nil || av.LatestProgress == nil {
		t.Fatalf("latest_progress missing: task=%+v agent=%+v", p, av.LatestProgress)
	}
	if p.Turn != 7 || !p.At.Equal(at) || p.LastTool != "bash" {
		t.Fatalf("progress fields: %+v", p)
	}
	if strings.Contains(p.Excerpt, progressSecret) || !strings.Contains(p.Excerpt, "[REDACTED]") {
		t.Fatalf("excerpt not redacted: %q", p.Excerpt)
	}
	if len(p.Excerpt) > MaxProgressExcerptBytes || !p.ExcerptTruncated || !strings.HasPrefix(p.Excerpt, "Found the key") {
		t.Fatalf("excerpt not bounded from the head: %d bytes, truncated=%v", len(p.Excerpt), p.ExcerptTruncated)
	}
	if !utf8.ValidString(p.Excerpt) {
		t.Fatalf("excerpt cut inside a rune: %q", p.Excerpt)
	}

	// Exposed over MCP: pi_status (task and agent) and pi_list_sessions.
	s := &MCPServer{M: m, Name: "devx-pi", Version: "test"}
	st := mcpCall(t, s, "pi_status", map[string]any{"task_id": tk.ID})
	lp := st["task"].(map[string]any)["latest_progress"].(map[string]any)
	if lp["turn"] != float64(7) || lp["last_tool"] != "bash" || lp["at"] == nil || strings.Contains(lp["excerpt"].(string), progressSecret) {
		t.Fatalf("pi_status task.latest_progress: %+v", lp)
	}
	if _, ok := st["agent"].(map[string]any)["latest_progress"]; !ok {
		t.Fatalf("pi_status agent.latest_progress missing: %+v", st["agent"])
	}
	ls := mcpCall(t, s, "pi_list_sessions", map[string]any{})
	ag := ls["agents"].([]any)[0].(map[string]any)
	if lp, ok := ag["latest_progress"].(map[string]any); !ok || lp["turn"] != float64(7) {
		t.Fatalf("pi_list_sessions latest_progress: %+v", ag)
	}
}

func TestLatestProgressDropsPartialTrailingWordOfBridgeCut(t *testing.T) {
	// The bridge cut its raw window inside a credential: the unrecognizable
	// fragment must not reach clients.
	v := progressView(1, time.Now(), "reading the token sk-ant-api03-ABCDEF", true, "")
	if strings.Contains(v.Excerpt, "sk-ant") || v.Excerpt != "reading the token" || !v.ExcerptTruncated {
		t.Fatalf("partial word kept: %+v", v)
	}
	if v := progressView(1, time.Now(), "complete sentence", false, ""); v.Excerpt != "complete sentence" || v.ExcerptTruncated {
		t.Fatalf("untruncated text changed: %+v", v)
	}
}

func TestLatestProgressAbsent(t *testing.T) {
	m, _ := newTestManager(t)
	a, tk := runningTaskWithBridge(t, m, BridgeProtocol)
	s := &MCPServer{M: m, Name: "devx-pi", Version: "test"}

	// Running, but no turn recorded yet.
	st := mcpCall(t, s, "pi_status", map[string]any{"task_id": tk.ID})
	if _, ok := st["task"].(map[string]any)["latest_progress"]; ok {
		t.Fatalf("latest_progress before any turn: %+v", st["task"])
	}
	if _, ok := st["agent"].(map[string]any)["latest_progress"]; ok {
		t.Fatalf("agent latest_progress before any turn: %+v", st["agent"])
	}

	// A waiting task never has progress, even if a stray file exists.
	w := sendT(t, m, a.ID, "queued", "kw")
	writeProgress(t, m, a.ID, Progress{TaskID: w.ID, Turn: 1, At: time.Now(), Excerpt: "x"})
	if tv, _, err := m.TaskStatus(w.ID); err != nil || tv.LatestProgress != nil {
		t.Fatalf("waiting task progress: %v %+v", err, tv)
	}

	// A record for another task, or an unreadable one, reads as none.
	writeProgress(t, m, a.ID, Progress{TaskID: w.ID, Turn: 1, At: time.Now(), Excerpt: "x"})
	if err := os.Rename(m.Store.progressPath(a.ID, w.ID), m.Store.progressPath(a.ID, tk.ID)); err != nil {
		t.Fatal(err)
	}
	if tv, av, _ := m.TaskStatus(tk.ID); tv.LatestProgress != nil || av.LatestProgress != nil {
		t.Fatalf("mismatched record used: %+v", tv.LatestProgress)
	}
	if err := os.WriteFile(m.Store.progressPath(a.ID, tk.ID), []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	if tv, _, err := m.TaskStatus(tk.ID); err != nil || tv.LatestProgress != nil {
		t.Fatalf("corrupt record: %v %+v", err, tv)
	}

	// The agent view reports progress only for its current task.
	writeProgress(t, m, a.ID, Progress{TaskID: tk.ID, Turn: 2, At: time.Now(), Excerpt: "working"})
	bridgeWrite(t, m, a.ID, tk.ID, func(x *Task) { now := time.Now(); x.State, x.FinishedAt = TaskCompleted, &now })
	tv, av, err := m.TaskStatus(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if av.LatestProgress != nil {
		t.Fatalf("idle agent reports progress: %+v", av.LatestProgress)
	}
	if tv.LatestProgress == nil || tv.LatestProgress.Turn != 2 {
		t.Fatalf("finished task keeps its last progress: %+v", tv.LatestProgress)
	}
}

func TestProgressEventsRedactedAndBounded(t *testing.T) {
	m, _ := newTestManager(t)
	a, tk := runningTaskWithBridge(t, m, BridgeProtocol)
	at := time.Now().UTC().Format(time.RFC3339Nano)
	if err := m.Store.WithAgentLock(a.ID, func() error {
		_, err := m.Store.AppendEvent(a.ID, Event{Type: EventProgress, TaskID: tk.ID, Source: "bridge", Data: map[string]any{
			"turn": 3, "at": at, "excerpt": "token=" + progressSecret + " " + strings.Repeat("y", 3000), "excerpt_truncated": false,
			"last_tool": "edit", "unexpected": strings.Repeat("z", 10000)}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s := &MCPServer{M: m, Name: "devx-pi", Version: "test"}
	out := mcpCall(t, s, "pi_events", map[string]any{"agent_id": a.ID, "after_seq": 0})
	var ev map[string]any
	for _, e := range out["events"].([]any) {
		if e.(map[string]any)["type"] == EventProgress {
			ev = e.(map[string]any)
		}
	}
	if ev == nil || ev["task_id"] != tk.ID {
		t.Fatalf("progress event not returned: %+v", out["events"])
	}
	d := ev["data"].(map[string]any)
	ex := d["excerpt"].(string)
	if strings.Contains(ex, progressSecret) || !strings.Contains(ex, "[REDACTED]") || len(ex) > MaxProgressExcerptBytes || d["excerpt_truncated"] != true {
		t.Fatalf("event excerpt not redacted/bounded: %d bytes %+v", len(ex), d["excerpt_truncated"])
	}
	if d["turn"] != float64(3) || d["at"] != at || d["last_tool"] != "edit" {
		t.Fatalf("event fields: %+v", d)
	}
	if _, ok := d["unexpected"]; ok {
		t.Fatal("unknown progress fields must not pass through")
	}
}

// Backward compatibility: a protocol-2 bridge (launched before progress)
// reports no progress, still supports running cancel, and is flagged as
// upgradeable; a pre-versioning bridge is still refused running cancel.
func TestOlderBridgesReportNoProgress(t *testing.T) {
	m, _ := newTestManager(t)
	a, tk := runningTaskWithBridge(t, m, BridgeCancelProtocol)
	tv, av, err := m.TaskStatus(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tv.LatestProgress != nil || av.LatestProgress != nil {
		t.Fatalf("old bridge reported progress: %+v", tv.LatestProgress)
	}
	if av.BridgeProtocol != 2 || !av.BridgeUpgradeNeeded {
		t.Fatalf("protocol-2 bridge should be flagged for upgrade: %+v", av)
	}
	s := &MCPServer{M: m, Name: "devx-pi", Version: "test"}
	st := mcpCall(t, s, "pi_status", map[string]any{"task_id": tk.ID})
	if _, ok := st["task"].(map[string]any)["latest_progress"]; ok {
		t.Fatal("pi_status must omit latest_progress for an old bridge")
	}
	if st["agent"].(map[string]any)["bridge_upgrade_needed"] != true {
		t.Fatalf("agent: %+v", st["agent"])
	}
	// Running cancel still works with protocol 2 (progress is additive).
	if _, err := m.Cancel(tk.ID); err != nil {
		t.Fatalf("protocol-2 running cancel refused: %v", err)
	}
	got, _ := m.Store.LoadTask(a.ID, tk.ID)
	if !got.CancelRequested || got.CancelOutcome(time.Now()) != CancelAbortPending {
		t.Fatalf("cancel on protocol-2 bridge: %+v outcome %s", got, got.CancelOutcome(time.Now()))
	}

	// Pre-versioning bridge: still refused, still no progress.
	m0, _ := newTestManager(t)
	_, tk0 := runningTaskWithBridge(t, m0, 0)
	if _, err := m0.Cancel(tk0.ID); err == nil {
		t.Fatal("protocol-0 running cancel must be refused")
	}
	if tv, _, _ := m0.TaskStatus(tk0.ID); tv.LatestProgress != nil {
		t.Fatal("protocol-0 bridge reported progress")
	}
}
