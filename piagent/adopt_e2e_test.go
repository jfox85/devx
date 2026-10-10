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

// fixtureAdopter adopts "human" sessions that live on the fixture tmux server.
type fixtureAdopter struct {
	*dirCreator
	human   map[string]string
	adopted map[string]string
}

func (c *fixtureAdopter) Adopt(name, agentID string) (AdoptedSession, error) {
	p, ok := c.human[name]
	if !ok {
		return AdoptedSession{}, Denied("unknown session %q", name)
	}
	if cur := c.adopted[name]; cur != "" && cur != agentID {
		return AdoptedSession{}, Denied("already managed by %s", cur)
	}
	c.adopted[name] = agentID
	return AdoptedSession{Name: name, Path: p, Project: "proj", TmuxName: name}, nil
}
func (c *fixtureAdopter) VerifyAdopted(name, agentID string) (AdoptedSession, error) {
	if c.adopted[name] != agentID {
		return AdoptedSession{}, Denied("not adopted by %s", agentID)
	}
	return AdoptedSession{Name: name, Path: c.human[name], Project: "proj", TmuxName: name}, nil
}
func (c *fixtureAdopter) ReleaseAdoption(name, agentID string) error {
	delete(c.adopted, name)
	return nil
}

// A human starts Pi WITHOUT the bridge in their own session and talks to it.
// adopt registers it with no tmux mutation; relaunch --force resumes the SAME
// Pi conversation with the bridge; after release, a queued remote task is
// delivered into that conversation. The human's earlier turn is preserved.
func TestE2EAdoptExistingHumanPiKeepsConversation(t *testing.T) {
	f := newPiFixture(t)
	base := filepath.Join(f.creator.base, "..", "human")
	wt := filepath.Join(base, "wt")
	if err := os.MkdirAll(wt, 0o700); err != nil {
		t.Fatal(err)
	}
	ad := &fixtureAdopter{dirCreator: f.creator, human: map[string]string{"humansess": wt}, adopted: map[string]string{}}
	f.m.Creator = ad

	// The human's own Pi: same isolated Pi config, fixed session id, no bridge.
	piSession := "01a1-adopt-e2e-0001"
	args := []string{f.m.Config.PiCommand}
	args = append(args, f.m.Config.PiCommandArgs...)
	args = append(args, "--session-id", piSession)
	args = append(args, f.m.Config.PiArgs...)
	script := filepath.Join(base, "human-pi.sh")
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shellQuote(a)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncd "+shellQuote(wt)+" || exit 1\nexec "+strings.Join(q, " ")+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.fx.NewSession("humansess", wt, script); err != nil {
		t.Fatal(err)
	}
	pane, err := f.tmux.Run("list-panes", "-t", "=humansess:", "-F", "#{pane_id}")
	if err != nil {
		t.Fatal(err)
	}
	pane = strings.TrimSpace(strings.Split(pane, "\n")[0])
	waitPane := func(want string) {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if out, _ := f.tmux.Capture(pane, 80); strings.Contains(out, want) {
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
		out, _ := f.tmux.Capture(pane, 80)
		t.Fatalf("pane never showed %q:\n%s", want, out)
	}
	time.Sleep(1500 * time.Millisecond) // TUI start
	type_ := func(text string) {
		if _, err := f.tmux.Run("send-keys", "-t", pane, "-l", text); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
		if _, err := f.tmux.Run("send-keys", "-t", pane, "Enter"); err != nil {
			t.Fatal(err)
		}
	}
	type_("human turn before adoption")
	waitPane("ECHO: human turn before adoption")

	r, err := f.m.Adopt(AdoptRequest{Session: "humansess", PaneID: pane, PiSessionID: piSession, By: "test"})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if v, _ := f.m.AgentStatus(r.AgentID); v.State != AgentAdoptedPending {
		t.Fatalf("state after adopt = %s (%s)", v.State, v.Detail)
	}
	// Queued remote work waits; the original Pi has no bridge.
	task := f.send(r.AgentID, "remote after adopt", "k-adopt-1")
	time.Sleep(1 * time.Second)
	if tv, _, _ := f.m.TaskStatus(task); tv.State != TaskWaiting {
		t.Fatalf("delivered before relaunch: %s", tv.State)
	}
	if err := f.m.Relaunch(r.AgentID, false); err == nil {
		t.Fatal("relaunch of an adopted Pi without --force must refuse")
	}
	if err := f.m.Relaunch(r.AgentID, true); err != nil {
		t.Fatalf("relaunch --force: %v", err)
	}
	v := f.online(r.AgentID)
	if v.Bridge.PiSessionID != piSession || v.Agent.Binding.PaneID != pane {
		t.Fatalf("bridge session %s pane %s", v.Bridge.PiSessionID, v.Agent.Binding.PaneID)
	}
	// Still human control: nothing delivered until release.
	time.Sleep(1 * time.Second)
	if tv, _, _ := f.m.TaskStatus(task); tv.State != TaskWaiting || tv.WaitingReason != WaitHumanControl {
		t.Fatalf("before release: state=%s reason=%s", tv.State, tv.WaitingReason)
	}
	if _, err := f.m.Release(r.AgentID, "test", false); err != nil {
		t.Fatal(err)
	}
	tv := f.waitTask(task, 45*time.Second, TaskCompleted, TaskFailed, TaskUnknown)
	if tv.State != TaskCompleted || !strings.HasPrefix(tv.Excerpt, "ECHO: remote after adopt") {
		t.Fatalf("task %+v", tv)
	}
	// Same conversation: the Pi session file holds the human turn AND the remote turn.
	file := v.Bridge.PiSessionFile
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("session file %q: %v", file, err)
	}
	var users []string
	for _, line := range strings.Split(string(data), "\n") {
		var e struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &e) == nil && e.Type == "message" && e.Message.Role == "user" {
			b, _ := json.Marshal(e.Message.Content)
			users = append(users, string(b))
		}
	}
	joined := strings.Join(users, "|")
	if !strings.Contains(joined, "human turn before adoption") || !strings.Contains(joined, "remote after adopt") {
		t.Fatalf("conversation not preserved; user turns: %s", joined)
	}
	if strings.Count(string(data), `"type":"session"`) != 1 {
		t.Fatalf("expected one session header (same conversation), file:\n%.400s", data)
	}
}
