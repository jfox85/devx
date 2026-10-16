package piagent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func eventRPCPage(t *testing.T, s *MCPServer, args any) map[string]any {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": "pi_events", "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	result, rpcErr := s.handle(rpcRequest{Method: "tools/call", Params: params})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > MaxEventsResponseBytes || !utf8.Valid(encoded) {
		t.Fatalf("unbounded or invalid event result: bytes=%d err=%v", len(encoded), err)
	}
	for _, id := range []json.RawMessage{json.RawMessage(`1`), json.RawMessage(`"same-id"`), json.RawMessage(`"` + strings.Repeat(`\u1234`, 512) + `"`)} {
		frame, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
		if err != nil || len(frame) > 65536 {
			t.Fatalf("unbounded RPC frame: bytes=%d err=%v", len(frame), err)
		}
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	text := out["content"].([]any)[0].(map[string]any)["text"].(string)
	var textData any
	if err := json.Unmarshal([]byte(text), &textData); err != nil || !reflect.DeepEqual(textData, out["structuredContent"]) {
		t.Fatal("text-only and structured clients see different event pages")
	}
	return out
}

func appendFixtureEvents(t *testing.T, m *Manager, agentID string, events []Event) {
	t.Helper()
	if err := m.Store.WithAgentLock(agentID, func() error {
		for _, ev := range events {
			if _, err := m.Store.AppendEvent(agentID, ev); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEventPageEncodedBudgetAndLosslessContinuation(t *testing.T) {
	for _, excerpt := range []string{strings.Repeat("x", 500), strings.Repeat("🙂\"\\\n<", 60)} {
		for _, limit := range []int{0, 200, 2} {
			m, _ := newTestManager(t)
			a := seedAgent(t, m)
			events := make([]Event, 205)
			for i := range events {
				events[i] = Event{Type: EventProgress, Source: "bridge", Data: map[string]any{
					"turn": i + 1, "at": time.Now().UTC().Format(time.RFC3339Nano), "excerpt": excerpt, "last_tool": "read"}}
			}
			appendFixtureEvents(t, m, a.ID, events)
			s := &MCPServer{M: m}
			after, pages := float64(0), 0
			for after < float64(len(events)) {
				args := map[string]any{"agent_id": a.ID, "after_seq": after}
				if limit != 0 {
					args["limit"] = limit
				}
				out := eventRPCPage(t, s, args)
				if out["isError"] != false {
					t.Fatalf("page failed: %+v", out)
				}
				page := out["structuredContent"].(map[string]any)
				rows := page["events"].([]any)
				if len(rows) == 0 || len(rows) > MaxEventsPage || (limit == 2 && len(rows) > 2) {
					t.Fatalf("bad count for limit %d: %d", limit, len(rows))
				}
				if pages == 0 && limit != 2 && len(rows) >= 200 {
					t.Fatal("reproduction must shorten a default/200-event page by bytes")
				}
				for _, row := range rows {
					ev := row.(map[string]any)
					after++
					if ev["seq"] != after || ev["data"].(map[string]any)["turn"] != after || ev["data"].(map[string]any)["excerpt"] != excerpt {
						t.Fatalf("event skipped, repeated or changed at seq %v: %+v", after, ev)
					}
				}
				if page["next_after_seq"] != after || page["last_seq"] != float64(len(events)) || page["more"] != (after < float64(len(events))) {
					t.Fatalf("incorrect cursor metadata: %+v", page)
				}
				if replay := eventRPCPage(t, s, args); !reflect.DeepEqual(replay, out) {
					t.Fatal("reconnecting at the same cursor changed the page")
				}
				pages++
			}
			t.Logf("limit=%d excerpt_bytes=%d: %d pages replay all %d events", limit, len(excerpt), pages, len(events))
			out := eventRPCPage(t, s, map[string]any{"agent_id": a.ID, "after_seq": after})
			page := out["structuredContent"].(map[string]any)
			if len(page["events"].([]any)) != 0 || page["next_after_seq"] != after || page["more"] != false {
				t.Fatalf("end-of-log page: %+v", page)
			}
		}
	}
}

func TestEventPageOversizedEventDoesNotAdvanceCursor(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	large := strings.Repeat("🙂\"\\\n", 20000)
	appendFixtureEvents(t, m, a.ID, []Event{
		{Type: "small", Source: "bridge"},
		{Type: "large", Source: "bridge", Data: map[string]any{"message": large}},
		{Type: "later", Source: "bridge"},
	})
	s := &MCPServer{M: m}
	first := eventRPCPage(t, s, map[string]any{"agent_id": a.ID})["structuredContent"].(map[string]any)
	if len(first["events"].([]any)) != 1 || first["next_after_seq"] != float64(1) || first["last_seq"] != float64(3) || first["more"] != true {
		t.Fatalf("must return fitting prefix before oversized event: %+v", first)
	}
	for i := 0; i < 2; i++ {
		out := eventRPCPage(t, s, map[string]any{"agent_id": a.ID, "after_seq": 1})
		page := out["structuredContent"].(map[string]any)
		if out["isError"] != true || page["error"] != "event_too_large" || page["event_seq"] != float64(2) || page["next_after_seq"] != float64(1) || page["last_seq"] != float64(3) || page["more"] != true {
			t.Fatalf("oversized event was skipped or not identified: %+v", out)
		}
	}
	// Paging must neither mutate nor truncate the durable payload.
	raw, _, err := m.Store.ReadEvents(a.ID, 1, 1)
	if err != nil || len(raw) != 1 || raw[0].Data["message"] != large {
		t.Fatalf("durable oversized event changed: %v", err)
	}
}

func TestEventPageErrorsAndEmptyLogAreBounded(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	s := &MCPServer{M: m}
	out := eventRPCPage(t, s, map[string]any{"agent_id": a.ID})
	page := out["structuredContent"].(map[string]any)
	if out["isError"] != false || len(page["events"].([]any)) != 0 || page["next_after_seq"] != float64(0) || page["last_seq"] != float64(0) || page["more"] != false {
		t.Fatalf("empty event log: %+v", out)
	}
	out = eventRPCPage(t, s, map[string]any{"agent_id": a.ID, strings.Repeat("\"🙂", 30000): true})
	if out["isError"] != true || out["structuredContent"].(map[string]any)["error"] != "failed" {
		t.Fatalf("unbounded diagnostic was not replaced: %+v", out)
	}
}
