package piagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An unusable owner launch policy refuses starts and (re)launches before any
// side effect, while read-only/control operations keep working.
func TestUnusableLaunchPolicyFailsClosed(t *testing.T) {
	m, creator := newTestManager(t)
	m.Config.LaunchPolicyErr = errors.New("synthetic: config is writable by group or others")
	_, err := m.Start(StartRequest{Project: "proj", SessionName: "s1", Prompt: "hi", IdempotencyKey: "k1"})
	if err == nil || !IsPermissionDenied(err) {
		t.Fatalf("start must be denied: %v", err)
	}
	if len(creator.existing) != 0 {
		t.Fatalf("start created a session: %v", creator.existing)
	}
	if agents, err := m.Store.ListAgents(); err != nil || len(agents) != 0 {
		t.Fatalf("start persisted agents: %v %v", agents, err)
	}
	if err := m.launch("pa_missing", true); err == nil || !IsPermissionDenied(err) {
		t.Fatalf("launch must be denied before loading the agent: %v", err)
	}
	if strings.Contains(m.launchPolicyUsable().Error(), "synthetic") {
		t.Fatalf("denial must not carry the load detail to MCP clients: %v", err)
	}
	// Relaunch of an existing (exited) agent is refused too.
	m.Config.LaunchPolicyErr = nil
	m.Tmux = Tmux{Exec: func(args ...string) (string, error) { return "", errors.New("can't find pane") }}
	a := &Agent{ID: "pa_0123456789ab", DevxSession: "s2", Project: "proj", Worktree: t.TempDir(), PiSessionID: "01a1-test",
		Lease: Lease{Holder: LeaseManaged, Generation: 1}}
	if err := m.Store.ensureAgentDirs(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.SaveAgent(a); err != nil {
		t.Fatal(err)
	}
	m.Config.LaunchPolicyErr = errors.New("synthetic")
	if err := m.Relaunch(a.ID, true); err == nil || !IsPermissionDenied(err) {
		t.Fatalf("relaunch must be denied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Store.AgentDir(a.ID), "launch.sh")); !os.IsNotExist(err) {
		t.Fatalf("relaunch wrote a launch script: %v", err)
	}
	if _, err := m.List(); err != nil {
		t.Fatalf("list must keep working: %v", err)
	}
}
