package piagent

import (
	"errors"
	"os"
	"testing"
)

// Integration (durability × protocol 2): while a relaunched bridge is still
// reconciling a predecessor's spooled receipt it writes no heartbeat. DevX
// must then (a) not orphan the spooled running task on status, and (b) refuse
// a cancel as transient bridge_unavailable, never bridge_upgrade_required
// (nothing is recorded, and a relaunch is not demanded).
func TestSilentRecoveryWindowUnderProtocol2Gating(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	liveBridge(t, m, a.ID, BridgeProtocol)
	tk := sendT(t, m, a.ID, "p", "k")
	bridgeWrite(t, m, a.ID, tk.ID, func(x *Task) { claim(x); accept(x) })
	// The old instance died after spooling; the relaunch has not heartbeated.
	if err := os.Remove(m.Store.bridgePath(a.ID)); err != nil {
		t.Fatal(err)
	}
	v, err := m.AgentStatus(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.BridgeOnline {
		t.Fatalf("no heartbeat must not be online: %+v", v)
	}
	got, err := m.Store.LoadTask(a.ID, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != TaskRunning {
		t.Fatalf("status during the silent window orphaned the task: %s", got.State)
	}
	_, err = m.Cancel(tk.ID)
	var be *BridgeUpgradeError
	if !errors.As(err, &be) || be.Kind != BridgeKindUnavailable {
		t.Fatalf("want transient bridge_unavailable, got %v", err)
	}
	got, _ = m.Store.LoadTask(a.ID, tk.ID)
	if got.CancelRequested {
		t.Fatal("a refused cancel must record nothing")
	}
	// Once recovered, the new instance heartbeats fresh at protocol 2.
	liveBridge(t, m, a.ID, BridgeProtocol)
	if v, _ := m.AgentStatus(a.ID); v.BridgeUpgradeNeeded {
		t.Fatal("a recovered protocol-2 bridge must not need an upgrade")
	}
}
