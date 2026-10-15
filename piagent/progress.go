package piagent

import (
	"strings"
	"time"
)

// EventProgress is the event type the bridge writes after a completed
// assistant turn of a running task (bridge protocol 3+; rate limited).
const EventProgress = "progress"

// progressView redacts and bounds raw bridge progress for clients. The
// excerpt is redacted before it is cut, so a credential is never split into
// an unrecognizable fragment: the bridge keeps a larger raw window than the
// MaxProgressExcerptBytes clients see.
func progressView(turn int, at time.Time, excerpt string, truncated bool, tool string) *ProgressView {
	if truncated {
		// The bridge cut the raw text; its last word may be a partial
		// credential that no pattern recognizes. Drop it.
		if i := strings.LastIndexAny(excerpt, " \t\r\n"); i >= 0 {
			excerpt = excerpt[:i]
		} else {
			excerpt = ""
		}
	}
	ex, cut := headExcerpt(Redact(excerpt), MaxProgressExcerptBytes)
	tool, _ = headExcerpt(Redact(strings.TrimSpace(tool)), MaxProgressToolBytes)
	return &ProgressView{At: at, Turn: turn, Excerpt: ex, ExcerptTruncated: truncated || cut, LastTool: tool}
}

// progressEventData normalizes a progress event's data the same way as
// latest_progress: only known fields, redacted and bounded.
func progressEventData(d map[string]any) map[string]any {
	str := func(k string) string { s, _ := d[k].(string); return s }
	turn := 0
	if f, ok := d["turn"].(float64); ok {
		turn = int(f)
	}
	trunc, _ := d["excerpt_truncated"].(bool)
	v := progressView(turn, time.Time{}, str("excerpt"), trunc, str("last_tool"))
	out := map[string]any{"turn": v.Turn, "excerpt": v.Excerpt}
	// Echo the bridge's timestamp only if it is one (bounded, no free text).
	if at, err := time.Parse(time.RFC3339Nano, str("at")); err == nil {
		out["at"] = at.UTC().Format(time.RFC3339Nano)
	}
	if v.ExcerptTruncated {
		out["excerpt_truncated"] = true
	}
	if v.LastTool != "" {
		out["last_tool"] = v.LastTool
	}
	return out
}
