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
			Description: "List DevX sessions running an MCP-managed Pi agent, with reconciled state (idle/running/human_control/unknown/binding_stale/pane_exited), who holds control, and how a human attaches. Also lists projects this server may start agents in.",
			InputSchema: obj(map[string]any{})},
		{Name: "pi_start_task", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			Description: "Create a NEW local-only DevX session (git worktree + tmux; no service ports, routes or project services) in an allowlisted project, launch an interactive Pi there, and queue the first prompt. Idempotent on idempotency_key: retrying after a timeout or dropped response returns the same agent_id/task_id and never starts a second session. The human can attach and take control at any time.",
			InputSchema: obj(map[string]any{
				"project":         strProp("Allowlisted DevX project alias"),
				"prompt":          strProp("Task for Pi (max 64 KiB)"),
				"idempotency_key": strProp("Caller-chosen unique key for this start; reuse it on retry"),
				"session_name":    strProp("Optional name for the new DevX session; must not already exist"),
			}, "project", "prompt", "idempotency_key")},
		{Name: "pi_send", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			Description: "Queue a follow-up prompt for a managed agent. Prompts are delivered one at a time when Pi is idle and DevX holds control; while a human has control they wait (waiting_reason=human_control). Idempotent on idempotency_key.",
			InputSchema: obj(map[string]any{
				"agent_id":        strProp("Managed agent id (pa_...)"),
				"prompt":          strProp("Follow-up prompt (max 64 KiB)"),
				"idempotency_key": strProp("Caller-chosen unique key for this send; reuse it on retry"),
			}, "agent_id", "prompt", "idempotency_key")},
		{Name: "pi_status", Annotations: ro,
			Description: "Status of a task (waiting/running/completed/failed/cancelled/unknown) and its agent, with a bounded result excerpt. 'unknown' means the task was delivered but the Pi process that held it is gone; inspect the session before retrying.",
			InputSchema: obj(map[string]any{"task_id": strProp("Task id (pt_...)")}, "task_id")},
		{Name: "pi_result", Annotations: ro,
			Description: "Read a task's final assistant text in bounded chunks. Continue from next_offset until eof. Text is passed through credential redaction.",
			InputSchema: obj(map[string]any{"task_id": strProp("Task id (pt_...)"), "offset": intProp("Byte offset (default 0)"), "max_bytes": intProp("Chunk size, max 16384")}, "task_id")},
		{Name: "pi_events", Annotations: ro,
			Description: "Durable, resumable agent event log. Pass after_seq = the last seq you processed; events are never dropped, so a reconnecting client replays exactly what it missed. wait_seconds (max 30) blocks until a new event is written instead of re-polling.",
			InputSchema: obj(map[string]any{"agent_id": strProp("Managed agent id"), "after_seq": intProp("Return events with seq greater than this"), "limit": intProp("Max events (<=200)"), "wait_seconds": intProp("Block up to N seconds for new events")}, "agent_id")},
		{Name: "pi_cancel", Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
			Description: "Cancel a task. Waiting tasks are cancelled immediately; running tasks are aborted in Pi. Denied while a human has control (the human owns running work).",
			InputSchema: obj(map[string]any{"task_id": strProp("Task id (pt_...)")}, "task_id")},
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
		instructions := "Manage Pi coding agents in DevX sessions. Every agent stays visible in DevX; a human can attach and take control at any time, which pauses delivery of your queued prompts until they release. Use idempotency keys and resume pi_events from your last seq."
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
			} else if errors.Is(err, ErrNotFound) {
				data = map[string]any{"error": "not_found", "message": msg}
			} else {
				data = map[string]any{"error": "failed", "message": Redact(msg)}
			}
			return toolResult(data, true), nil
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
		if err := decodeArgs(raw, &struct{}{}); err != nil {
			return nil, err
		}
		views, err := s.M.List()
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(views))
		for _, v := range views {
			out = append(out, agentSummary(v))
		}
		return map[string]any{"agents": out, "allowed_projects": s.M.Config.AllowedProjects}, nil

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
			"attach": "devx session attach " + r.Session}, nil

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
		return map[string]any{"task_id": r.TaskID, "replayed": r.Replayed, "task": tv, "agent": agentSummary(av)}, nil

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
		next := w.AfterSeq
		if n := len(evs); n > 0 {
			next = evs[n-1].Seq
		}
		if evs == nil {
			evs = []Event{}
		}
		return map[string]any{"events": evs, "next_after_seq": next, "last_seq": last, "more": next < last}, nil

	case "pi_cancel":
		var w struct {
			TaskID string `json:"task_id"`
		}
		if err := decodeArgs(raw, &w); err != nil {
			return nil, err
		}
		if _, err := s.M.Cancel(w.TaskID); err != nil {
			return nil, err
		}
		tv, av, err := s.M.TaskStatus(w.TaskID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": tv, "agent": agentSummary(av)}, nil
	}
	return nil, denied("unknown tool %q", name)
}

func agentSummary(v *AgentView) map[string]any {
	a := v.Agent
	return map[string]any{
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
	}
}
