package piagent

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// MaxSessionListBytes bounds the complete UTF-8 encoded MCP CallToolResult,
// including both compatibility text and structuredContent. This leaves over
// 5 KiB for JSON-RPC framing below a 64 KiB gateway result limit.
const MaxSessionListBytes = 60000

// Keep the same JSON in text and structuredContent for clients that only read
// text (MCP structured-content backward compatibility). Compact encoding avoids
// the previous formatted-text overhead without hiding data from those clients.
func sessionListResult(data any) map[string]any {
	b, _ := json.Marshal(data)
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": data,
		"isError":           false,
	}
}

// Even a malformed request with an extremely long unknown field name must
// produce a bounded error. Preserve normal typed errors; never truncate JSON.
func sessionListError(data any) map[string]any {
	result := sessionListResult(data)
	result["isError"] = true
	encoded, err := json.Marshal(result)
	if err == nil && len(encoded) <= MaxSessionListBytes {
		return result
	}
	result = sessionListResult(map[string]any{"error": "failed", "message": "session inventory request failed; diagnostic exceeds result budget"})
	result["isError"] = true
	return result
}

type sessionListCursor struct {
	Version   int    `json:"v"`
	Inventory string `json:"inventory"`
	Offset    int    `json:"offset"`
}

func encodeSessionCursor(inventory string, offset int) string {
	b, _ := json.Marshal(sessionListCursor{1, inventory, offset})
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *MCPServer) listSessions(raw json.RawMessage) (any, error) {
	var args struct {
		Limit  *int   `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArgs(raw, &args); err != nil {
		return nil, err
	}
	limit := 20
	if args.Limit != nil {
		limit = *args.Limit
	}
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}
	if len(args.Cursor) > 512 {
		return nil, fmt.Errorf("invalid session cursor; restart without cursor")
	}
	agents, err := s.M.Store.ListAgents()
	if err != nil {
		return nil, err
	}
	// IDs are stable even when state, leases or retirement change. Membership
	// changes are detected rather than silently shifting an offset to skip rows.
	sort.Slice(agents, func(i, j int) bool { return agents[i].ID < agents[j].ID })
	projects := append([]string{}, s.M.Config.AllowedProjects...)
	sort.Strings(projects)
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	membership, _ := json.Marshal(struct{ Agents, Projects []string }{ids, projects})
	hash := sha256.Sum256(membership)
	inventory := hex.EncodeToString(hash[:])
	offset := 0
	total := len(agents) + len(projects)
	if args.Cursor != "" {
		b, decodeErr := base64.RawURLEncoding.DecodeString(args.Cursor)
		var cursor sessionListCursor
		if decodeErr != nil || json.Unmarshal(b, &cursor) != nil || cursor.Version != 1 || cursor.Offset <= 0 || cursor.Offset >= total {
			return nil, fmt.Errorf("invalid session cursor; restart without cursor")
		}
		if cursor.Inventory != inventory {
			return nil, fmt.Errorf("session inventory changed; restart without cursor")
		}
		offset = cursor.Offset
	}
	out := make([]map[string]any, 0)
	allowed := make([]string, 0)
	page := func(next int) map[string]any {
		cursor := ""
		if next < total {
			cursor = encodeSessionCursor(inventory, next)
		}
		return map[string]any{"agents": out, "allowed_projects": allowed, "next_cursor": cursor, "eof": next == total,
			"total_agents": len(agents), "total_allowed_projects": len(projects)}
	}
	next := offset
	for next < total && next-offset < limit {
		isAgent := next < len(agents)
		if isAgent {
			view, statusErr := s.M.AgentStatus(agents[next].ID)
			if statusErr != nil {
				return nil, statusErr
			}
			out = append(out, agentSummary(view))
		} else {
			allowed = append(allowed, projects[next-len(agents)])
		}
		encoded, marshalErr := json.Marshal(sessionListResult(page(next + 1)))
		if marshalErr != nil {
			return nil, fmt.Errorf("cannot encode session inventory")
		}
		if len(encoded) > MaxSessionListBytes {
			if isAgent {
				out = out[:len(out)-1]
			} else {
				allowed = allowed[:len(allowed)-1]
			}
			if next == offset {
				return nil, fmt.Errorf("session inventory entry exceeds %d encoded bytes; no entries skipped", MaxSessionListBytes)
			}
			break
		}
		next++
	}
	return page(next), nil
}
