//go:build !windows

package piagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real interactive Pi: a relaunched bridge reconciles a predecessor's spooled
// terminal receipt before it announces itself. The spool stands in for a turn
// the killed Pi settled but could not publish (fault injection of that write
// lives in testdata/bridge-durability.test.mjs). A structurally invalid spool
// next to it is quarantined instead of blocking dispatch.
func TestE2ERelaunchPublishesSpooledReceiptBeforeDispatch(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("warm up", "k1")
	f.waitTask(r.TaskID, 45*time.Second, TaskCompleted)
	running := f.send(r.AgentID, "crash during SLOW", "k2")
	f.waitTask(running, 30*time.Second, TaskRunning)
	v := f.online(r.AgentID)
	tk, err := f.m.Store.LoadTask(r.AgentID, running)
	if err != nil {
		t.Fatal(err)
	}
	// SIGKILL: no shutdown hook. Plant what the dead instance would have
	// spooled had its turn settled with publication failing, before devx
	// reconciles anything.
	if err := killPID(v.Bridge.PID); err != nil {
		t.Fatal(err)
	}
	spool := map[string]any{
		"v": 2, "receipt_id": "rc_00000000000000aa", "task_id": running, "instance": tk.BridgeInstance,
		"text": "SPOOLED-OUTCOME", "stop": "stop", "aborted": false, "abort_on_request": false,
		"lease_generation_basis": v.Agent.Lease.Generation, "settled_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	writeSpool := func(id string, body any) {
		data, _ := json.Marshal(body)
		if err := os.WriteFile(filepath.Join(f.m.Store.AgentDir(r.AgentID), "results", id+".receipt.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSpool(running, spool)
	writeSpool("pt_00000000000000ff", map[string]any{"v": 2, "receipt_id": "rc_00000000000000bb", "task_id": "pt_00000000000000ff"})
	after := f.send(r.AgentID, "after relaunch", "k3")
	// devx's own reconciliation marks the running task unknown once the
	// dead process is noticed (a Go-side decision this bridge PR does not
	// change); the spool must then be attached, not lost or rewritten.
	f.waitTask(running, 20*time.Second, TaskUnknown)
	if err := f.m.Relaunch(r.AgentID, false); err != nil {
		t.Fatal(err)
	}
	if tv := f.waitTask(after, 45*time.Second, TaskCompleted, TaskFailed); tv.State != TaskCompleted {
		t.Fatalf("%+v", tv)
	}
	text, ok, err := f.m.Store.ReadResult(r.AgentID, running)
	if err != nil || !ok || !strings.Contains(text, "SPOOLED-OUTCOME") {
		t.Fatalf("spooled result not attached: ok=%v err=%v text=%q", ok, err, text)
	}
	if tv, _, _ := f.m.TaskStatus(running); tv.State != TaskUnknown {
		t.Fatalf("a terminal state must never be rewritten: %s", tv.State)
	}
	var recovered, quarantined, online int
	onlineBeforeRecovered := false
	for _, e := range f.events(r.AgentID) {
		switch e.Type {
		case "task_result_recovered":
			if e.TaskID == running {
				recovered++
			}
		case "receipt_unreadable":
			quarantined++
		case "bridge_online":
			online++
			if recovered == 0 && online > 1 {
				onlineBeforeRecovered = true
			}
		}
	}
	if recovered != 1 || quarantined != 1 {
		t.Fatalf("recovered=%d quarantined=%d", recovered, quarantined)
	}
	if onlineBeforeRecovered {
		t.Fatal("the relaunched bridge announced itself before reconciling the spool")
	}
	dir := filepath.Join(f.m.Store.AgentDir(r.AgentID), "results")
	if _, err := os.Stat(filepath.Join(dir, running+".receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("spool should be removed after recovery: %v", err)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "pt_00000000000000ff.receipt.json.*.corrupt")); len(m) != 1 {
		t.Fatalf("invalid spool should be kept aside once: %v", m)
	}
}
