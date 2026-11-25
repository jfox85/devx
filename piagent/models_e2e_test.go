//go:build !windows

package piagent

import (
	"errors"
	"testing"
	"time"
)

// Real interactive Pi (faux provider with faux-1 and faux-2; the fixture's
// pi_args default to faux-1). Model validation uses the real
// `pi --list-models` through the same launch command.
func TestE2EModelSelectionWithRealPi(t *testing.T) {
	f := newPiFixture(t)

	// Invalid model: rejected by the real catalog, nothing started.
	_, err := f.m.Start(StartRequest{Project: "proj", Prompt: "hi", IdempotencyKey: "bad", Model: "devx-faux/faux-404"})
	var me *ModelError
	if !errors.As(err, &me) || len(me.Valid) == 0 {
		t.Fatalf("want ModelError listing valid models, got %v", err)
	}
	if agents, _ := f.m.Store.ListAgents(); len(agents) != 0 {
		t.Fatal("invalid model started an agent")
	}

	// Start on faux-2 (overrides the pi_args default of faux-1).
	r, err := f.m.Start(StartRequest{Project: "proj", Prompt: "first", IdempotencyKey: "k1", Model: "devx-faux/faux-2"})
	if err != nil {
		t.Fatal(err)
	}
	tv := f.waitTask(r.TaskID, 60*time.Second, TaskCompleted, TaskFailed)
	if tv.State != TaskCompleted || tv.Model != "devx-faux/faux-2" {
		t.Fatalf("start task: %+v\npane:\n%s", tv, f.pane(r.AgentID))
	}
	if v := f.online(r.AgentID); v.Model != "devx-faux/faux-2" {
		t.Fatalf("agent model: %q", v.Model)
	}

	// Switch with pi_send: the task runs on faux-1, and the session stays there.
	taskID, err := f.m.Send(SendRequest{AgentID: r.AgentID, Prompt: "second", IdempotencyKey: "s1", Model: "faux-1"})
	if err != nil {
		t.Fatal(err)
	}
	tv = f.waitTask(taskID.TaskID, 60*time.Second, TaskCompleted, TaskFailed)
	if tv.State != TaskCompleted || tv.Model != "devx-faux/faux-1" || tv.RequestedModel != "devx-faux/faux-1" {
		t.Fatalf("switched task: %+v\npane:\n%s", tv, f.pane(r.AgentID))
	}
	f.waitAgent(r.AgentID, 10*time.Second, func(v *AgentView) bool { return v.Model == "devx-faux/faux-1" }, "model faux-1 reported")
	if a, _ := f.m.Store.LoadAgent(r.AgentID); a.Model != "devx-faux/faux-1" {
		t.Fatalf("agent record not updated for relaunch: %q", a.Model)
	}

	// A relaunch resumes on the switched model.
	if err := f.m.Relaunch(r.AgentID, true); err != nil {
		t.Fatal(err)
	}
	f.waitAgent(r.AgentID, 30*time.Second, func(v *AgentView) bool { return v.BridgeOnline && v.Model == "devx-faux/faux-1" }, "relaunched on faux-1")
}

// Without a model, Pi uses its configured default (here faux-1 from pi_args).
func TestE2EDefaultModelWithRealPi(t *testing.T) {
	f := newPiFixture(t)
	r := f.start("hello", "k1")
	tv := f.waitTask(r.TaskID, 60*time.Second, TaskCompleted, TaskFailed)
	if tv.State != TaskCompleted || tv.Model != "devx-faux/faux-1" {
		t.Fatalf("task: %+v", tv)
	}
	if r.Model != "" {
		t.Fatalf("launch model should be empty for the default: %q", r.Model)
	}
}
