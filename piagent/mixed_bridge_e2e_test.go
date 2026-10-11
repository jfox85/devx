//go:build !windows

package piagent

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Mixed-version validation (fixture-only): an agent whose Pi was launched
// with the pre-versioning bridge (testdata/devx-bridge-protocol1.ts) while
// this DevX build serves MCP. Restarting the MCP server does not refresh a
// running Pi's bridge, so this is the state every existing agent is in after
// an upgrade until a human relaunches it.
func newOldBridgeFixture(t *testing.T, extraEnv ...string) *piFixture {
	t.Helper()
	f := newPiFixture(t, extraEnv...)
	src, err := os.ReadFile("testdata/devx-bridge-protocol1.ts")
	if err != nil {
		t.Fatal(err)
	}
	f.m.bridgeSrc = src
	return f
}

// The hazard the gate prevents, demonstrated against the real old bridge:
// a cancel recorded on an unaccepted hand-off (as an ungated DevX would) is
// ignored by the old bridge, which starts the prompt anyway.
func TestE2EOldBridgeIgnoresPreAcceptanceCancel(t *testing.T) {
	f := newOldBridgeFixture(t, "DEVX_PI_TEST_DELIVERY_DELAY_MS=3000")
	r := f.start("warmup", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	if v := f.online(r.AgentID); v.BridgeProtocol != 0 || !v.BridgeUpgradeNeeded {
		t.Fatalf("old bridge should report protocol 0 and need upgrade: %+v", v)
	}
	task := f.send(r.AgentID, "old bridge HAZARD-PROMPT", "k2")
	f.waitTask(task, 15*time.Second, TaskRunning) // claimed, inside the delay
	// Record the cancel the way the ungated code did (bypassing the gate).
	if err := f.m.Store.WithAgentLock(r.AgentID, func() error {
		tk, err := f.m.Store.LoadTask(r.AgentID, task)
		if err != nil {
			return err
		}
		tk.CancelRequested = true
		return f.m.Store.SaveTask(tk)
	}); err != nil {
		t.Fatal(err)
	}
	tv := f.waitTask(task, 45*time.Second, TaskCompleted, TaskCancelled, TaskFailed)
	// Pi accepted and ran the "cancelled" prompt (it may have been aborted
	// once running, but it was never dropped before acceptance).
	if tv.AcceptedAt == nil || tv.CancelOutcome == CancelBeforeAcceptance {
		t.Fatalf("expected the old bridge to start the prompt: %+v", tv)
	}
}

// With the gate, the same cancel is refused with an explicit receipt and
// nothing is recorded; the old bridge delivers normally and the human's
// control is untouched. Waiting work stays cancellable.
func TestE2ERunningCancelRefusedOnOldBridge(t *testing.T) {
	f := newOldBridgeFixture(t, "DEVX_PI_TEST_DELIVERY_DELAY_MS=3000")
	r := f.start("warmup", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	f.online(r.AgentID)
	task := f.send(r.AgentID, "gated prompt", "k2")
	waiting := f.send(r.AgentID, "still queued", "k3")
	before := f.waitTask(task, 15*time.Second, TaskRunning)
	if _, err := f.m.Takeover(r.AgentID, "test-human"); err != nil {
		t.Fatal(err)
	}
	_, err := f.m.Cancel(task)
	var be *BridgeUpgradeError
	if !errors.As(err, &be) || be.Protocol != 0 {
		t.Fatalf("want bridge_upgrade_required for protocol 0, got %v", err)
	}
	if after, _, _ := f.m.TaskStatus(task); after.CancelRequested || after.Revision != before.Revision {
		t.Fatalf("refused cancel was recorded: %+v", after)
	}
	if _, err := f.m.Cancel(waiting); err != nil {
		t.Fatalf("waiting cancel on old bridge: %v", err)
	}
	// The old bridge fences the hand-off back under human control: no abort.
	tv := f.waitTask(task, 15*time.Second, TaskWaiting, TaskCancelled, TaskCompleted)
	if tv.State != TaskWaiting || tv.WaitingReason != WaitHumanControl {
		t.Fatalf("after takeover on old bridge: %+v", tv)
	}
	if strings.Contains(eventTypes(f.events(r.AgentID)), "abort") {
		t.Fatalf("abort on old bridge: %s", eventTypes(f.events(r.AgentID)))
	}
	// A human relaunch loads the current bridge, which reports protocol 2.
	f.m.bridgeSrc = nil
	if err := f.m.Relaunch(r.AgentID, true); err != nil {
		t.Fatal(err)
	}
	v := f.waitAgent(r.AgentID, 30*time.Second, func(v *AgentView) bool { return v.BridgeOnline && v.BridgeProtocol == BridgeProtocol }, "relaunched bridge protocol")
	if v.BridgeUpgradeNeeded {
		t.Fatalf("still needs upgrade after relaunch: %+v", v)
	}
}
