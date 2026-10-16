package piagent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// MCP over stdio (newline-delimited JSON-RPC 2.0). No network listener is
// opened. The tool set is a fixed allowlist; there is no shell or arbitrary
// command tool, no takeover/release (human-only), and no access to sessions
// that DevX did not create through this interface.

const mcpProtocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

// Tool is an MCP tool definition contributed by a ToolProvider.
type Tool = tool

// ToolProvider contributes optional tools (for example the artifact bridge)
// to the MCP server. Providers own their argument validation, authorization
// and result shape; the server only routes calls to them.
type ToolProvider interface {
	// Tools returns the provider's currently enabled tools. Disabled
	// capabilities must not be listed.
	Tools() []Tool
	// CallTool returns a complete MCP tool result, or handled=false when
	// name is not one of the provider's enabled tools.
	CallTool(name string, args json.RawMessage) (result map[string]any, handled bool)
	// TaskArtifacts lists artifacts produced while a task ran, for
	// inclusion in pi_status. It returns nil when not applicable or not
	// authorized; it never fails the status call.
	TaskArtifacts(agent *Agent, task *TaskView) []map[string]any
	// Instructions is appended to the initialize instructions when non-empty.
	Instructions() string
}

// Obj builds a closed JSON object schema (exported for tool providers).
func Obj(props map[string]any, required ...string) map[string]any { return obj(props, required...) }

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var (
	strProp = func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	intProp = func(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
)

func toolList() []tool {
	ro := map[string]any{"readOnlyHint": true, "openWorldHint": false}
	return []tool{
		{Name: "pi_list_sessions", Annotations: ro,
			Description: "List a bounded page of all MCP-managed Pi agent records (including retired agents), with reconciled state, control and attach instructions, then allowed projects. Follow next_cursor until eof, even when agents is empty. Inventory membership changes invalidate cursors; restart without cursor. State is live per page, not a snapshot. Each agent with a running task may include latest_progress (see pi_status).",
			InputSchema: obj(map[string]any{
				"limit":  map[string]any{"type": "integer", "description": "Maximum combined agent and project entries (default 20, max 100); byte budget may shorten a page", "minimum": 1, "maximum": 100},
				"cursor": map[string]any{"type": "string", "description": "Opaque next_cursor from the previous page; omit to start", "maxLength": 512},
			})},
		{Name: "pi_start_task", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			Description: "Create a NEW local-only DevX session (git worktree + tmux; no service ports, routes or project services) in an allowlisted project, launch an interactive Pi there, and queue the first prompt. Idempotent on idempotency_key: retrying after a timeout or dropped response returns the same agent_id/task_id and never starts a second session. The human can attach and take control at any time.",
			InputSchema: obj(map[string]any{
				"project":         strProp("Allowlisted DevX project alias"),
				"prompt":          strProp("Task for Pi (max 64 KiB)"),
				"idempotency_key": strProp("Caller-chosen unique key for this start; reuse it on retry"),
				"session_name":    strProp("Optional name for the new DevX session; must not already exist"),
			}, "project", "prompt", "idempotency_key")},
		{Name: "pi_send", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			Description: "QUEUE a follow-up prompt for a managed agent. This never steers or interrupts a running turn: the prompt waits in the DevX queue and is delivered as a new turn, one at a time, only when Pi is idle, its editor is empty and DevX holds control (while a human has control it waits with waiting_reason=human_control). The response reports delivery=queued; follow phase queued -> delivered -> accepted -> finished with pi_status/pi_queue/pi_events. Until delivered it can be edited (pi_update_pending), replaced (pi_supersede_pending) or cancelled (pi_cancel). Idempotent on idempotency_key.",
			InputSchema: obj(map[string]any{
				"agent_id":        strProp("Managed agent id (pa_...)"),
				"prompt":          strProp("Follow-up prompt (max 64 KiB)"),
				"idempotency_key": strProp("Caller-chosen unique key for this send; reuse it on retry"),
			}, "agent_id", "prompt", "idempotency_key")},
		{Name: "pi_status", Annotations: ro,
			Description: "Status of a task and its agent, with a bounded result excerpt. state is waiting/running/completed/failed/cancelled/unknown; phase separates queued (DevX queue), delivered (handed to Pi, not yet accepted), accepted (Pi started the turn) and finished. revision is the compare-and-swap token for pending edits. cancel_outcome reports what a cancellation did. 'unknown' means the task was delivered but the Pi process that held it is gone; inspect the session before retrying. Mid-task visibility: task.latest_progress {at, turn, excerpt, excerpt_truncated, last_tool} is the newest recorded assistant turn of the task (redacted; excerpt <= 500 bytes; recorded at most every 30 s per task). It is absent until a turn is recorded and always absent when agent.bridge_protocol < 3 (an older Pi bridge; a human relaunch loads the new one). agent.latest_progress is the same for the agent's current_task.",
			InputSchema: obj(map[string]any{"task_id": strProp("Task id (pt_...)")}, "task_id")},
		{Name: "pi_result", Annotations: ro,
			Description: "Read a task's final assistant text in bounded chunks. Continue from next_offset until eof. Text is passed through credential redaction.",
			InputSchema: obj(map[string]any{"task_id": strProp("Task id (pt_...)"), "offset": intProp("Byte offset (default 0)"), "max_bytes": intProp("Chunk size, max 16384")}, "task_id")},
		{Name: "pi_events", Annotations: ro,
			Description: "Durable, resumable agent event log. Pass after_seq = the last seq you processed; events are never dropped, so a reconnecting client replays exactly what it missed. wait_seconds (max 30) blocks until a new event is written instead of re-polling. Pages contain at most 200 events and 60,000 encoded MCP result bytes (including text and structuredContent); bytes may shorten a page. Continue from next_after_seq while more is true. An individually oversized event returns event_too_large with event_seq and an unchanged next_after_seq: inspect the local log; no events are skipped. Besides lifecycle events (task_delivered, task_started, task_finished, lease_changed, ...), Pi bridges with protocol >= 3 write type=progress events while a task runs: data {turn, at, excerpt (redacted, <= 500 bytes), excerpt_truncated?, last_tool?}, at most one per task every 30 s with the newest turn kept.",
			InputSchema: obj(map[string]any{"agent_id": strProp("Managed agent id"), "after_seq": intProp("Return events with seq greater than this"), "limit": intProp("Max events (<=200)"), "wait_seconds": intProp("Block up to N seconds for new events")}, "agent_id")},
		{Name: "pi_cancel", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
			Description: "Cancel a task. Queued tasks are cancelled immediately (cancel_outcome=cancelled_before_delivery). A task handed to Pi but not yet accepted is dropped before Pi starts it. For an accepted (running) turn this REQUESTS an abort: the bridge aborts it once, only if DevX still holds control at the same lease generation and Pi has no internally queued messages; outcome moves abort_pending -> abort_sent -> aborted, or abort_withheld (abort_withheld = human_control / control_changed_since_request / pi_has_queued_messages) / finished_before_abort / outcome_unknown (the Pi process went away) / abort_unconfirmed (no settle within 30 s; nothing is escalated). abort_sent means the abort was dispatched, not that anything stopped; aborted means Pi's agent loop settled the turn, NOT that every subprocess a tool or extension started has exited. Denied while a human has control of accepted running work (a task Pi has not accepted yet can still be dropped). Cancelling a running task requires the agent's live Pi bridge to speak the current protocol; otherwise nothing is recorded and error=bridge_upgrade_required (older bridge; restarting this server does not refresh a running Pi, a human relaunch does) or error=bridge_unavailable (no live heartbeat from the Pi holding the task; may be transient: retry or inspect, do not relaunch blindly). Optional expected_revision makes the cancel compare-and-swap. Repeating it is a no-op, except that a withheld abort can be requested again after a human released control.",
			InputSchema: obj(map[string]any{
				"task_id":           strProp("Task id (pt_...)"),
				"agent_id":          strProp("Optional agent id; when given the task must belong to it"),
				"expected_revision": intProp("Optional revision from pi_status/pi_queue; the cancel fails with conflict if the task changed"),
			}, "task_id")},
		{Name: "pi_queue", Annotations: ro,
			Description: "Inspect an agent's queue: in-flight work, pending prompts in delivery order (with revision, phase, waiting_reason, age and a redacted preview) and recent outcomes. Use the revision with pi_update_pending, pi_supersede_pending or pi_cancel.",
			InputSchema: obj(map[string]any{"agent_id": strProp("Managed agent id (pa_...)"), "recent": intProp("Recent finished tasks to include (max 10)")}, "agent_id")},
		{Name: "pi_update_pending", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
			Description: "Replace the prompt of a task that is still queued (not yet handed to Pi), keeping its task id and queue position. Compare-and-swap: expected_revision must match the current revision, otherwise the call fails with error=conflict and the current revision/state and nothing changes. Once delivered a prompt can no longer be edited. Idempotent on idempotency_key (keys are never forgotten); a task allows at most 32 in-place edits, after which use pi_supersede_pending.",
			InputSchema: obj(map[string]any{
				"agent_id":          strProp("Managed agent id (pa_...)"),
				"task_id":           strProp("Queued task id (pt_...)"),
				"prompt":            strProp("New prompt (max 64 KiB)"),
				"expected_revision": intProp("Revision you read from pi_queue/pi_status"),
				"idempotency_key":   strProp("Caller-chosen unique key for this edit; reuse it on retry"),
			}, "agent_id", "task_id", "prompt", "expected_revision", "idempotency_key")},
		{Name: "pi_supersede_pending", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
			Description: "Atomically cancel a still-queued task and queue a replacement in the same queue position (new task id; the old one records superseded_by). Same compare-and-swap and idempotency rules as pi_update_pending. Never affects work already handed to Pi.",
			InputSchema: obj(map[string]any{
				"agent_id":          strProp("Managed agent id (pa_...)"),
				"task_id":           strProp("Queued task id (pt_...) to replace"),
				"prompt":            strProp("Replacement prompt (max 64 KiB)"),
				"expected_revision": intProp("Revision you read from pi_queue/pi_status"),
				"idempotency_key":   strProp("Caller-chosen unique key for this edit; reuse it on retry"),
			}, "agent_id", "task_id", "prompt", "expected_revision", "idempotency_key")},
	}
}

// MCPServer serves the tools over one stdio connection.
type MCPServer struct {
	M       *Manager
	Name    string
	Version string
	Client  string // identity recorded on agents created through this server
	// Extra optionally contributes additional tools (nil = none).
	Extra ToolProvider
}

// Serve reads requests until EOF. Responses are written as they complete;
// tool calls run concurrently so a blocking pi_events does not stall others.
func (s *MCPServer) Serve(in io.Reader, out io.Writer) error {
	var mu sync.Mutex
	write := func(v any) {
		b, _ := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(append(b, '\n'))
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			write(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}})
			continue
		}
		if len(req.ID) == 0 {
			continue // notification
		}
		respond := func(req rpcRequest) {
			result, rerr := s.handle(req)
			resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
			if rerr != nil {
				resp.Error = rerr
			} else {
				resp.Result = result
			}
			write(resp)
		}
		if req.Method != "tools/call" {
			respond(req) // cheap; keeps initialize/list ordered
			continue
		}
		wg.Add(1)
		go func(req rpcRequest) {
			defer wg.Done()
			respond(req)
		}(req)
	}
	return sc.Err()
}

func (s *MCPServer) handle(req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		instructions := "Manage Pi coding agents in DevX sessions. Every agent stays visible in DevX; a human can attach and take control at any time, which pauses delivery of your queued prompts until they release. pi_send only queues: it never steers a running turn, and queued is not delivered, accepted or completed — check phase/state before relying on a result. Use idempotency keys, compare-and-swap revisions for pending edits, and resume pi_events from your last seq."
		if s.Extra != nil {
			if extra := s.Extra.Instructions(); extra != "" {
				instructions += " " + extra
			}
		}
		return map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    instructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := toolList()
		if s.Extra != nil {
			tools = append(tools, s.Extra.Tools()...)
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		if s.Extra != nil {
			if result, handled := s.Extra.CallTool(p.Name, p.Arguments); handled {
				return result, nil
			}
		}
		data, err := s.call(p.Name, p.Arguments)
		if err != nil {
			msg := err.Error()
			if IsPermissionDenied(err) {
				data = map[string]any{"error": "permission_denied", "message": msg}
			} else if ee := (*eventPageSizeError)(nil); errors.As(err, &ee) {
				data = map[string]any{"error": "event_too_large", "message": msg, "event_seq": ee.Seq,
					"next_after_seq": ee.After, "last_seq": ee.Last, "more": true}
			} else if be := (*BridgeUpgradeError)(nil); errors.As(err, &be) {
				d := map[string]any{"error": be.Kind, "message": msg, "task_id": be.TaskID, "agent_id": be.AgentID,
					"required_protocol": BridgeCancelProtocol, "recorded": false}
				if be.Protocol >= 0 {
					d["bridge_protocol"] = be.Protocol
				}
				data = d
			} else if errors.Is(err, ErrEditLimit) {
				data = map[string]any{"error": "edit_limit_reached", "message": msg, "max_edits": MaxEditsPerTask,
					"hint": "use pi_supersede_pending to replace the task"}
			} else if ce := (*ConflictError)(nil); errors.As(err, &ce) {
				data = map[string]any{"error": "conflict", "message": msg, "task_id": ce.TaskID, "expected_revision": ce.Expected,
					"current_revision": ce.Current, "state": ce.State, "phase": ce.Phase}
			} else if errors.Is(err, ErrNotFound) {
				data = map[string]any{"error": "not_found", "message": msg}
			} else {
				data = map[string]any{"error": "failed", "message": Redact(msg)}
			}
			if p.Name == "pi_list_sessions" {
				return sessionListError(data), nil
			}
			if p.Name == "pi_events" {
				return eventPageError(data), nil
			}
			return toolResult(data, true), nil
		}
		if p.Name == "pi_list_sessions" {
			return sessionListResult(data), nil
		}
		return toolResult(data, false), nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
}

func toolResult(data any, isErr bool) map[string]any {
	b, _ := json.MarshalIndent(data, "", "  ")
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": data,
		"isError":           isErr,
	}
}

func decodeArgs(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func (s *MCPServer) call(name string, raw json.RawMessage) (any, error) {
	switch name {
	case "pi_list_sessions":
		return s.listSessions(raw)

	case "pi_start_task":
		var a struct {
			Project, Prompt, IdempotencyKey, SessionName string
		}
		var w struct {
			Project        string `json:"project"`
			Prompt         string `json:"prompt"`
			IdempotencyKey string `json:"idempotency_key"`
			SessionName    string `json:"session_name"`
		}
		if err := decodeArgs(raw, &w); err != nil {
			return nil, err
		}
		a.Project, a.Prompt, a.IdempotencyKey, a.SessionName = w.Project, w.Prompt, w.IdempotencyKey, w.SessionName
		r, err := s.M.Start(StartRequest{Project: a.Project, SessionName: a.SessionName, Prompt: a.Prompt, IdempotencyKey: a.IdempotencyKey, By: s.Client})
		if err != nil {
			return nil, err
		}
		return map[string]any{"agent_id": r.AgentID, "task_id": r.TaskID, "session": r.Session, "pi_session_id": r.PiSessionID, "replayed": r.Replayed,
			"delivery": "queued", "attach": "devx session attach " + r.Session}, nil

	case "pi_send":
		var w struct {
			AgentID        string `json:"agent_id"`
			Prompt         string `json:"prompt"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if err := decodeArgs(raw, &w); err != nil {
			return nil, err
		}
		r, err := s.M.Send(SendRequest{AgentID: w.AgentID, Prompt: w.Prompt, IdempotencyKey: w.IdempotencyKey})
		if err != nil {
			return nil, err
		}
		tv, av, err := s.M.TaskStatus(r.TaskID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task_id": r.TaskID, "replayed": r.Replayed, "delivery": "queued", "task": tv, "agent": agentSummary(av)}, nil

	case "pi_status":
		var w struct {
			TaskID string `json:"task_id"`
		}
		if err := decodeArgs(raw, &w); err != nil {
			return nil, err
		}
		tv, av, err := s.M.TaskStatus(w.TaskID)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"task": tv, "agent": agentSummary(av)}
		if s.Extra != nil {
			if arts := s.Extra.TaskArtifacts(av.Agent, tv); arts != nil {
				out["artifacts"] = arts
			}
		}
		return out, nil

	case "pi_result":
		var w struct {
			TaskID   string `json:"task_id"`
			Offset   int    `json:"offset"`
			MaxBytes int    `json:"max_bytes"`
		}
		if err := decodeArgs(raw, &w); err != nil {
			return nil, err
		}
		return s.M.Result(w.TaskID, w.Offset, w.MaxBytes)

	case "pi_events":
		var w struct {
			AgentID     string `json:"agent_id"`
			AfterSeq    int64  `json:"after_seq"`
			Limit       int    `json:"limit"`
			WaitSeconds int    `json:"wait_seconds"`
		}
		if err := decodeArgs(raw, &w); err != nil {
			return nil, err
		}
		evs, last, err := s.M.Events(w.AgentID, w.AfterSeq, w.Limit, time.Duration(w.WaitSeconds)*time.Second)
		if err != nil {
			return nil, err
		}
		return eventPage(evs, w.AfterSeq, last)

	case "pi_cancel":
		var w struct {
			TaskID           string `json:"task_id"`
			AgentID          string `json:"agent_id"`
			ExpectedRevision *int64 `json:"expected_revision"`
		}
		if err := decodeArgs(raw, &w); err != nil {
			return nil, err
		}
		if _, err := s.M.CancelTask(CancelRequest{AgentID: w.AgentID, TaskID: w.TaskID, ExpectedRevision: w.ExpectedRevision}); err != nil {
			return nil, err
		}
		tv, av, err := s.M.TaskStatus(w.TaskID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": tv, "cancel_outcome": tv.CancelOutcome, "agent": agentSummary(av)}, nil

	case "pi_queue":
		var w struct {
			AgentID string `json:"agent_id"`
			Recent  int    `json:"recent"`
		}
		if err := decodeArgs(raw, &w); err != nil {
			return nil, err
		}
		return s.M.Queue(w.AgentID, w.Recent)

	case "pi_update_pending", "pi_supersede_pending":
		var w struct {
			AgentID          string `json:"agent_id"`
			TaskID           string `json:"task_id"`
			Prompt           string `json:"prompt"`
			ExpectedRevision *int64 `json:"expected_revision"`
			IdempotencyKey   string `json:"idempotency_key"`
		}
		if err := decodeArgs(raw, &w); err != nil {
			return nil, err
		}
		if w.ExpectedRevision == nil {
			return nil, fmt.Errorf("expected_revision is required (read it with pi_queue or pi_status)")
		}
		var r *EditResult
		var err error
		if name == "pi_update_pending" {
			r, err = s.M.UpdatePending(UpdatePendingRequest{AgentID: w.AgentID, TaskID: w.TaskID, Prompt: w.Prompt, ExpectedRevision: *w.ExpectedRevision, IdempotencyKey: w.IdempotencyKey})
		} else {
			r, err = s.M.SupersedePending(SupersedePendingRequest{AgentID: w.AgentID, TaskID: w.TaskID, Prompt: w.Prompt, ExpectedRevision: *w.ExpectedRevision, IdempotencyKey: w.IdempotencyKey})
		}
		if err != nil {
			return nil, err
		}
		// Report the current state, not an assumed one: a replayed edit may
		// find the task already delivered or finished.
		out := map[string]any{"task_id": r.TaskID, "revision": r.Revision, "replayed": r.Replayed}
		if tv, _, err := s.M.TaskStatus(r.TaskID); err == nil {
			out["task"] = tv
		}
		if r.ReplacementTaskID != "" {
			out["replacement_task_id"] = r.ReplacementTaskID
			if tv, _, err := s.M.TaskStatus(r.ReplacementTaskID); err == nil {
				out["replacement"] = tv
			}
		}
		return out, nil
	}
	return nil, denied("unknown tool %q", name)
}

func agentSummary(v *AgentView) map[string]any {
	a := v.Agent
	out := map[string]any{
		"agent_id":       a.ID,
		"session":        a.DevxSession,
		"project":        a.Project,
		"pi_session_id":  a.PiSessionID,
		"state":          v.State,
		"detail":         v.Detail,
		"control":        a.Lease.Holder,
		"lease":          a.Lease,
		"bridge_online":  v.BridgeOnline,
		"current_task":   v.CurrentTask,
		"waiting_tasks":  v.Waiting,
		"last_event_seq": v.LastSeq,
		"pane":           a.Binding.PaneID,
		"attach":         v.AttachHint,
		// A running Pi keeps the bridge it was launched with; restarting
		// this server does not refresh it.
		"bridge_protocol":       v.BridgeProtocol,
		"bridge_upgrade_needed": v.BridgeUpgradeNeeded,
	}
	// Newest mid-task progress of current_task (redacted, at most
	// MaxProgressExcerptBytes); absent when none was reported.
	if v.LatestProgress != nil {
		out["latest_progress"] = v.LatestProgress
	}
	return out
}
