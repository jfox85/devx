package artifactbridge

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jfox85/devx/piagent"
)

const (
	ToolList   = "devx_artifact_list"
	ToolRead   = "devx_artifact_read"
	ToolUpload = "devx_attachment_upload"
)

var _ piagent.ToolProvider = (*Service)(nil)

func str(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
func integer(d string, min, max int64) map[string]any {
	return map[string]any{"type": "integer", "description": d, "minimum": min, "maximum": max}
}

// Tools lists the capabilities enabled by the current policy (re-read on
// every tools/list). Definitions are constant text so a policy change never
// alters an approved tool definition; an unreadable policy lists nothing.
func (s *Service) Tools() []piagent.Tool {
	p, err := s.policy()
	if err != nil {
		return nil
	}
	ro := map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": false}
	var out []piagent.Tool
	if p.Read {
		out = append(out,
			piagent.Tool{Name: ToolList, Annotations: ro,
				Description: "List files explicitly registered as DevX artifacts (reports, screenshots, attachments) in one managed agent's session. Returns opaque artifact ids (dxa_...), title, MIME type, size, content version and SHA-256. Only registered artifacts are visible; there is no general file access.",
				InputSchema: piagent.Obj(map[string]any{
					"agent_id": str("Managed agent id (pa_...) from pi_list_sessions"),
					"type":     str("Optional artifact type filter (report, screenshot, document, log, diff, plan, recording, other)"),
					"search":   str("Optional text filter over title, summary, tags and id"),
					"cursor":   integer("Pagination cursor from next_cursor", 0, 100000),
					"limit":    integer("Max items (1-50)", 1, MaxListPage),
				}, "agent_id")},
			piagent.Tool{Name: ToolRead, Annotations: ro,
				Description: fmt.Sprintf("Read one registered artifact by id. Text is returned in UTF-8-safe chunks (max %d bytes; continue from next_offset until eof). Small PNG/JPEG/GIF/WebP images (<= %d bytes) are returned as an MCP image block; any other content as base64 chunks. Pass version from devx_artifact_list to fail with version_mismatch instead of mixing chunks from changed content. Verify the whole file with checksums.sha256.", MaxReadChunk, MaxInlineImageBytes),
				InputSchema: piagent.Obj(map[string]any{
					"agent_id":    str("Managed agent id (pa_...)"),
					"artifact_id": str("Artifact id (dxa_...) from devx_artifact_list"),
					"version":     str("Optional expected content version (c_...)"),
					"offset":      integer("Byte offset (default 0)", 0, MaxReadableBytes),
					"max_bytes":   integer(fmt.Sprintf("Chunk size (max %d)", MaxReadChunk), 1, MaxReadChunk),
					"encoding":    map[string]any{"type": "string", "enum": []string{"auto", "text", "base64", "image"}, "description": "auto (default), text, base64 or image"},
				}, "agent_id", "artifact_id")})
	}
	if p.Upload {
		out = append(out, piagent.Tool{Name: ToolUpload,
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			Description: fmt.Sprintf("Upload a reference file (PNG, JPEG, GIF, WebP, PDF, text, Markdown, CSV or JSON; up to the owner's configured limit, at most %d bytes) into a managed agent's session attachments (.artifacts/attachments/, never repository files). Only sessions the owner explicitly listed for uploads accept them. Send sequential chunks of <= %d bytes as base64, each with the same idempotency_key, filename, mime_type, size and sha256 and the offset it starts at. The file is verified (size, SHA-256, content type) and registered when the last byte arrives; the response gives the artifact id and the manifest id the local Pi agent can resolve. Retrying any call is safe; call without data_base64 to get next_offset after an interruption.", MaxUploadBytesCeiling, MaxUploadChunk),
			InputSchema: piagent.Obj(map[string]any{
				"agent_id":        str("Managed agent id (pa_...)"),
				"idempotency_key": str("Caller-chosen unique key for this file; reuse it for every chunk and on retry"),
				"filename":        str("Plain file name with an extension matching mime_type, e.g. mockup.png"),
				"mime_type":       str("image/png, image/jpeg, image/gif, image/webp, application/pdf, text/plain, text/markdown, text/csv or application/json"),
				"size":            integer("Total file size in bytes", 1, MaxUploadBytesCeiling),
				"sha256":          str("Lowercase hex SHA-256 of the whole file"),
				"offset":          integer("Byte offset where this chunk starts", 0, MaxUploadBytesCeiling),
				"data_base64":     str(fmt.Sprintf("Chunk bytes, standard base64 (<= %d decoded bytes). Omit to query progress.", MaxUploadChunk)),
				"title":           str("Optional title (default: filename)"),
				"summary":         str("Optional description for the agent"),
			}, "agent_id", "idempotency_key", "filename", "mime_type", "size", "sha256", "offset")})
	}
	return out
}

// Instructions describes the artifact tools when any are enabled.
func (s *Service) Instructions() string {
	p, err := s.policy()
	if err != nil || (!p.Read && !p.Upload) {
		return ""
	}
	return "Exchange files with an agent only through registered DevX artifacts: list them with devx_artifact_list, read by id with devx_artifact_read, and send reference files with devx_attachment_upload. pi_status lists artifacts produced during a task."
}

// CallTool routes the bridge tools. A bridge tool whose capability is off, or
// any bridge tool while the policy is unreadable, is answered with an error
// (never executed); non-bridge names are not handled here.
func (s *Service) CallTool(name string, raw json.RawMessage) (map[string]any, bool) {
	if name != ToolList && name != ToolRead && name != ToolUpload {
		return nil, false
	}
	p, err := s.policy()
	if err != nil {
		return errorResult(err), true
	}
	if (name == ToolUpload && !p.Upload) || (name != ToolUpload && !p.Read) {
		return errorResult(errf(codeDenied, "tool %s is not enabled", name)), true
	}
	switch name {
	case ToolList:
		var w struct {
			AgentID string `json:"agent_id"`
			Type    string `json:"type"`
			Search  string `json:"search"`
			Cursor  int    `json:"cursor"`
			Limit   int    `json:"limit"`
		}
		if err := decode(raw, &w); err != nil {
			return errorResult(err), true
		}
		out, err := s.List(ListRequest{AgentID: w.AgentID, Type: w.Type, Search: w.Search, Cursor: w.Cursor, Limit: w.Limit})
		if err != nil {
			return errorResult(err), true
		}
		return jsonResult(out), true

	case ToolRead:
		var w struct {
			AgentID    string `json:"agent_id"`
			ArtifactID string `json:"artifact_id"`
			Version    string `json:"version"`
			Offset     int64  `json:"offset"`
			MaxBytes   int    `json:"max_bytes"`
			Encoding   string `json:"encoding"`
		}
		if err := decode(raw, &w); err != nil {
			return errorResult(err), true
		}
		r, err := s.Read(ReadRequest{AgentID: w.AgentID, ArtifactID: w.ArtifactID, Version: w.Version, Offset: w.Offset, MaxBytes: w.MaxBytes, Encoding: w.Encoding})
		if err != nil {
			return errorResult(err), true
		}
		return readResult(r), true

	case ToolUpload:
		var w struct {
			AgentID        string  `json:"agent_id"`
			IdempotencyKey string  `json:"idempotency_key"`
			Filename       string  `json:"filename"`
			MIMEType       string  `json:"mime_type"`
			Size           int64   `json:"size"`
			SHA256         string  `json:"sha256"`
			Offset         int64   `json:"offset"`
			DataBase64     *string `json:"data_base64"`
			Title          string  `json:"title"`
			Summary        string  `json:"summary"`
		}
		if err := decode(raw, &w); err != nil {
			return errorResult(err), true
		}
		out, err := s.Upload(UploadRequest{AgentID: w.AgentID, IdempotencyKey: w.IdempotencyKey, Filename: w.Filename, MIMEType: w.MIMEType,
			Size: w.Size, SHA256: w.SHA256, Offset: w.Offset, DataBase64: w.DataBase64, Title: w.Title, Summary: w.Summary})
		if err != nil {
			return errorResult(err), true
		}
		return jsonResult(out), true
	}
	return nil, false
}

func decode(raw json.RawMessage, dst any) error {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	if len(raw) > 64<<10 {
		return errf(codeTooLarge, "arguments are too large")
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errf(codeInvalid, "invalid arguments")
	}
	return nil
}

func errorResult(err error) map[string]any {
	e := asError(err)
	data := map[string]any{"error": e.Code, "message": e.Msg}
	for k, v := range e.Extra {
		data[k] = v
	}
	return jsonResultErr(data, true)
}

func jsonResult(data any) map[string]any { return jsonResultErr(data, false) }

func jsonResultErr(data any, isErr bool) map[string]any {
	b, _ := json.Marshal(data)
	res := map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(b)}},
		"structuredContent": data,
		"isError":           isErr,
	}
	return boundResult(res)
}

// boundResult enforces MaxResultBytes on the encoded result. Listing and
// metadata never approach it; it is a guard, not a truncation strategy.
func boundResult(res map[string]any) map[string]any {
	if b, err := json.Marshal(res); err == nil && len(b) <= MaxResultBytes {
		return res
	}
	data := map[string]any{"error": codeTooLarge, "message": "result exceeds the transport limit; use a smaller limit or max_bytes"}
	b, _ := json.Marshal(data)
	return map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": data, "isError": true}
}

// readResult carries the payload once: in structuredContent (text or base64)
// for programmatic clients, and as a human/model-readable content block. Text
// chunks are not duplicated in a JSON text block, so a full 16 KiB chunk
// stays well within the gateway's 64 KiB result limit.
func readResult(r *ReadResult) map[string]any {
	meta := *r
	var content []map[string]any
	switch r.Encoding {
	case "image":
		// The image travels once, in the content block; structuredContent
		// carries metadata only.
		content = []map[string]any{{"type": "image", "data": base64.StdEncoding.EncodeToString(r.image), "mimeType": r.Artifact.MIMEType}}
	case "text":
		content = []map[string]any{{"type": "text", "text": *r.Text}}
	default:
		hdr, _ := json.Marshal(map[string]any{"artifact_id": r.Artifact.ID, "encoding": r.Encoding, "offset": r.Offset, "length": r.Length, "eof": r.EOF})
		content = []map[string]any{{"type": "text", "text": string(hdr) + "\n(base64 data is in structuredContent.data_base64)"}}
	}
	return boundResult(map[string]any{"content": content, "structuredContent": &meta, "isError": false})
}

// TaskArtifacts returns artifacts registered in the agent's session while the
// task ran (from delivery to finish, plus a short grace period), excluding
// remote attachments. It returns nil when reading is disabled, the agent is
// not authorized, or the task was never delivered.
func (s *Service) TaskArtifacts(agent *piagent.Agent, task *piagent.TaskView) []map[string]any {
	if agent == nil || task == nil || task.DeliveredAt == nil {
		return nil
	}
	sc, err := s.authorize(agent.ID, capRead)
	if err != nil {
		return nil
	}
	m, err := s.manifest(sc)
	if err != nil {
		return nil
	}
	from := task.DeliveredAt.Add(-2 * time.Second)
	to := s.Now()
	if task.FinishedAt != nil {
		to = task.FinishedAt.Add(30 * time.Second)
	}
	out := []map[string]any{}
	for _, a := range m.Artifacts {
		if a.Created.Before(from) || a.Created.After(to) || hasTag(a.Tags, AttachmentTag) {
			continue
		}
		if len(out) >= MaxTaskArtifacts {
			break
		}
		out = append(out, map[string]any{"id": opaqueID(sc.agent.ID, sc.sess.Name, a.ID), "manifest_id": a.ID, "title": a.Title,
			"type": a.Type, "mime_type": mimeForFile(a.File), "created": a.Created})
	}
	return out
}

func hasTag(tags []string, t string) bool {
	for _, x := range tags {
		if x == t {
			return true
		}
	}
	return false
}
