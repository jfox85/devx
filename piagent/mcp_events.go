package piagent

import (
	"encoding/json"
	"fmt"
)

// MaxEventsResponseBytes bounds the complete encoded MCP CallToolResult,
// including compatibility text, structuredContent and JSON escaping. Reserve
// over 5 KiB for normal JSON-RPC framing below a 64 KiB gateway limit;
// arbitrarily long caller-supplied request IDs are outside this result budget.
const MaxEventsResponseBytes = 60000

type eventPageSizeError struct {
	Seq, After, Last int64
}

func (e *eventPageSizeError) Error() string {
	return fmt.Sprintf("event %d exceeds %d encoded bytes; no events skipped; inspect the agent's local event log", e.Seq, MaxEventsResponseBytes)
}

// eventPage returns only a prefix: the continuation cursor must never pass an
// event that did not fit. An oversized first event fails without advancing the
// cursor or echoing its payload; the durable log remains available locally.
func eventPage(events []Event, after, last int64) (map[string]any, error) {
	if events == nil {
		events = []Event{}
	}
	page := func(n int) map[string]any {
		next := after
		if n > 0 {
			next = events[n-1].Seq
		}
		return map[string]any{"events": events[:n], "next_after_seq": next, "last_seq": last, "more": next < last}
	}
	for n := 1; n <= len(events); n++ {
		encoded, err := json.Marshal(toolResult(page(n), false))
		if err != nil {
			return nil, fmt.Errorf("cannot encode event page")
		}
		if len(encoded) > MaxEventsResponseBytes {
			if n == 1 {
				return nil, &eventPageSizeError{Seq: events[0].Seq, After: after, Last: last}
			}
			return page(n - 1), nil
		}
	}
	return page(len(events)), nil
}

// Malformed arguments and store errors must obey the same wire budget, even
// when their diagnostic includes an arbitrarily long caller-supplied string.
func eventPageError(data any) map[string]any {
	result := toolResult(data, true)
	if encoded, err := json.Marshal(result); err == nil && len(encoded) <= MaxEventsResponseBytes {
		return result
	}
	return toolResult(map[string]any{"error": "failed", "message": "event request failed; diagnostic exceeds result budget; no events skipped"}, true)
}
