//go:build !windows

package piagent

import (
	"strings"
	"testing"
	"time"
)

// A real interactive Pi (faux model) running a multi-turn task: the bridge
// records each completed assistant turn as progress (rate limit disabled
// here), and devx serves it redacted through pi_events and pi_status.
func TestE2EProgressFromRealPi(t *testing.T) {
	f := newPiFixture(t, "DEVX_PI_TEST_PROGRESS_INTERVAL_MS=0")
	r := f.start("MULTITURN please", "k1")
	tv := f.waitTask(r.TaskID, 60*time.Second, TaskCompleted, TaskFailed)
	if tv.State != TaskCompleted {
		t.Fatalf("task: %+v\npane:\n%s", tv, f.pane(r.AgentID))
	}
	evs, _, err := f.m.Events(r.AgentID, 0, MaxEventsPage, 0)
	if err != nil {
		t.Fatal(err)
	}
	var prog []Event
	finished := -1
	for i, e := range evs {
		if e.Type == EventProgress && e.TaskID == r.TaskID {
			prog = append(prog, e)
		}
		if e.Type == "task_finished" && e.TaskID == r.TaskID {
			finished = i
		}
	}
	// Steps 1 and 2 are recorded while running; the final turn may settle
	// before a tick, in which case the result supersedes it.
	if len(prog) < 2 {
		t.Fatalf("want >= 2 progress events, got %s\npane:\n%s", eventTypes(evs), f.pane(r.AgentID))
	}
	for i, e := range prog[:2] {
		ex, _ := e.Data["excerpt"].(string)
		if e.Data["turn"] != i+1 || !strings.HasPrefix(ex, "step "+string(rune('1'+i))) || e.Data["last_tool"] != "read" {
			t.Fatalf("progress %d: %+v", i, e.Data)
		}
		if strings.Contains(ex, "ghp_") {
			t.Fatalf("progress excerpt not redacted: %q", ex)
		}
	}
	if finished < 0 {
		t.Fatalf("no task_finished: %s", eventTypes(evs))
	}
	if tv.LatestProgress == nil || tv.LatestProgress.Turn < 2 {
		t.Fatalf("pi_status latest_progress: %+v", tv.LatestProgress)
	}
	if v := f.online(r.AgentID); v.BridgeProtocol != BridgeProtocol || v.BridgeUpgradeNeeded {
		t.Fatalf("bridge protocol: %+v", v)
	}
}

// An agent launched with the pre-versioning bridge keeps working and
// simply reports no progress.
func TestE2EOldBridgeReportsNoProgress(t *testing.T) {
	f := newOldBridgeFixture(t, "DEVX_PI_TEST_PROGRESS_INTERVAL_MS=0")
	r := f.start("MULTITURN please", "k1")
	tv := f.waitTask(r.TaskID, 60*time.Second, TaskCompleted, TaskFailed)
	if tv.State != TaskCompleted || tv.LatestProgress != nil {
		t.Fatalf("old bridge task: %+v", tv)
	}
	for _, e := range f.events(r.AgentID) {
		if e.Type == EventProgress {
			t.Fatalf("old bridge wrote progress: %+v", e)
		}
	}
}
