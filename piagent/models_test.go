package piagent

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var testModels = []string{"anthropic/claude-sonnet-4-5", "openai/gpt-5", "openai-codex/gpt-5", "devx-faux/faux-1"}

// mcpCallRaw returns the whole tool result, errors included.
func mcpCallRaw(t *testing.T, s *MCPServer, tool string, args map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	return byID(mcpRoundTrip(t, s, string(b)), 1)["result"].(map[string]any)
}

// withModels makes m validate against a fixed catalog and counts lookups.
func withModels(m *Manager, models []string, err error) *int {
	calls := 0
	m.listModels = func() ([]string, error) { calls++; return models, err }
	return &calls
}

func TestParseListModels(t *testing.T) {
	out := "Warning: errors loading models.json:\nboom\n" +
		"\x1b[1mprovider\x1b[0m   model                     context  max-out  thinking  images\n" +
		"anthropic  claude-sonnet-4-5         200K     64K      yes       yes\n" +
		"devx-faux  faux-1                    128K     65.5K    no        no\n\n"
	got := parseListModels(out)
	if strings.Join(got, ",") != "anthropic/claude-sonnet-4-5,devx-faux/faux-1" {
		t.Fatalf("parsed %v", got)
	}
	if got := parseListModels(`No models matching "x"`); len(got) != 0 {
		t.Fatalf("no header must yield nothing: %v", got)
	}
}

func TestResolveModelRef(t *testing.T) {
	cases := []struct{ in, want, reason string }{
		{"anthropic/claude-sonnet-4-5", "anthropic/claude-sonnet-4-5", ""},
		{"Anthropic/Claude-Sonnet-4-5", "anthropic/claude-sonnet-4-5", ""},
		{"claude-sonnet-4-5", "anthropic/claude-sonnet-4-5", ""}, // unique bare id
		{"gpt-5", "", "ambiguous"},
		{"anthropic/claude-sonnet-9", "", "unknown_or_unavailable"},
		{"nope", "", "unknown_or_unavailable"},
	}
	for _, c := range cases {
		got, err := resolveModelRef(c.in, testModels)
		var me *ModelError
		switch {
		case c.reason == "" && (err != nil || got != c.want):
			t.Errorf("%q: got %q, %v; want %q", c.in, got, err, c.want)
		case c.reason != "" && (!errors.As(err, &me) || me.Reason != c.reason || len(me.Valid) != len(testModels)):
			t.Errorf("%q: want %s ModelError with valid models, got %q, %v", c.in, c.reason, got, err)
		}
	}
	_, err := resolveModelRef("anthropic/claude-sonnet-9", testModels)
	if me := (*ModelError)(nil); !errors.As(err, &me) || len(me.Suggestions) == 0 || me.Suggestions[0] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("want a close suggestion, got %+v", err)
	}
}

func TestStartWithValidModelLaunchesOnIt(t *testing.T) {
	m, _ := newTestManager(t)
	calls := withModels(m, testModels, nil)
	// Launch fails at tmux (disabled in unit tests); everything before it,
	// including the recorded model, is durable and retried with the key.
	_, err := m.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k", Model: "claude-sonnet-4-5"})
	if err == nil || !strings.Contains(err.Error(), "tmux") {
		t.Fatalf("expected the tmux launch failure, got %v", err)
	}
	agents, _ := m.Store.ListAgents()
	if len(agents) != 1 || agents[0].Model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("agent model not recorded canonically: %+v", agents)
	}
	script := m.launchScript(agents[0], "n", "/b.ts")
	if !strings.Contains(script, "'--model' 'anthropic/claude-sonnet-4-5'") {
		t.Fatalf("launch script lacks --model:\n%s", script)
	}
	// The model comes after the owner's pi_args, so it wins.
	m.Config.PiArgs = []string{"--model", "owner/default"}
	script = m.launchScript(agents[0], "n", "/b.ts")
	if strings.Index(script, "'owner/default'") > strings.Index(script, "'anthropic/claude-sonnet-4-5'") {
		t.Fatalf("agent model must follow pi_args:\n%s", script)
	}
	// Same key + same model (in any accepted spelling) replays; a different
	// model is a different request.
	_, err = m.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k", Model: "anthropic/claude-sonnet-4-5"})
	if err != nil && strings.Contains(err.Error(), "different start request") {
		t.Fatalf("same model must replay: %v", err)
	}
	_, err = m.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k", Model: "devx-faux/faux-1"})
	if err == nil || !strings.Contains(err.Error(), "different start request") {
		t.Fatalf("a different model must not replay: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("the model catalog should be cached, listed %d times", *calls)
	}
}

// A retry is judged against the recorded request, never the live catalog:
// a catalog that fails or changed later must not report a started task as
// "nothing started" (which would invite a duplicate under a new key).
func TestReplayDoesNotRevalidateModel(t *testing.T) {
	m, _ := newTestManager(t)
	withModels(m, testModels, nil)
	_, _ = m.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k", Model: "claude-sonnet-4-5"})
	agents, _ := m.Store.ListAgents()
	if len(agents) != 1 {
		t.Fatalf("want 1 agent, got %d", len(agents))
	}
	// The start task carries its model so the bridge verifies it.
	if tk, err := m.Store.LoadTask(agents[0].ID, agents[0].StartTaskID); err != nil || tk.Model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("start task must carry the model: %+v %v", tk, err)
	}
	withModels(m, nil, errors.New("catalog down"))
	m.models = modelCatalog{}
	for _, spelling := range []string{"claude-sonnet-4-5", "anthropic/claude-sonnet-4-5"} {
		_, err := m.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k", Model: spelling})
		var ce *ModelCatalogError
		var me *ModelError
		if errors.As(err, &ce) || errors.As(err, &me) || (err != nil && strings.Contains(err.Error(), "different start request")) {
			t.Fatalf("%q: replay must not revalidate: %v", spelling, err)
		}
	}
	if _, err := m.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k", Model: "openai/gpt-5"}); err == nil || !strings.Contains(err.Error(), "different start request") {
		t.Fatalf("a different model under the same key must be refused: %v", err)
	}
	if _, err := m.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k"}); err == nil || !strings.Contains(err.Error(), "different start request") {
		t.Fatalf("dropping the model under the same key must be refused: %v", err)
	}

	// Same for pi_send.
	m2, _ := newTestManager(t)
	withModels(m2, testModels, nil)
	a := seedAgent(t, m2)
	liveBridge(t, m2, a.ID, BridgeProtocol)
	r, err := m2.Send(SendRequest{AgentID: a.ID, Prompt: "x", IdempotencyKey: "s", Model: "openai/gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	withModels(m2, nil, errors.New("catalog down"))
	m2.models = modelCatalog{}
	r2, err := m2.Send(SendRequest{AgentID: a.ID, Prompt: "x", IdempotencyKey: "s", Model: "openai/gpt-5"})
	if err != nil || r2.TaskID != r.TaskID || !r2.Replayed || r2.Model != "openai/gpt-5" {
		t.Fatalf("send replay: %+v %v", r2, err)
	}
}

func TestReplayAcceptsSlashModelIDs(t *testing.T) {
	valid := []string{"openrouter/team/model-v1"}
	got, err := resolveModelRef("team/model-v1", valid)
	if err != nil || got != "openrouter/team/model-v1" {
		t.Fatalf("resolve: %q %v", got, err)
	}
	for _, req := range []string{"team/model-v1", "openrouter/team/model-v1", "TEAM/MODEL-V1"} {
		if !sameModelRequest(req, got) {
			t.Errorf("%q must replay as %q", req, got)
		}
	}
	for _, req := range []string{"model-v1", "other/team/model-v1", ""} {
		if sameModelRequest(req, got) {
			t.Errorf("%q must not replay as %q", req, got)
		}
	}
}

func TestIsRelativeCommandPath(t *testing.T) {
	for cmd, want := range map[string]bool{"pi": false, "/usr/bin/pi": false, "./bin/pi": true, "bin/pi": true, `.\pi.exe`: true} {
		if got := isRelativeCommandPath(cmd); got != want {
			t.Errorf("%q: got %v want %v", cmd, got, want)
		}
	}
}

func TestRelativePiCommandCannotValidateModels(t *testing.T) {
	m, _ := newTestManager(t)
	m.Config.PiCommand = "./bin/pi"
	_, err := m.AvailableModels()
	var ce *ModelCatalogError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "relative path") {
		t.Fatalf("want a relative-path catalog error, got %v", err)
	}
}

func TestCatalogErrorWrapsSubprocessError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	m, _ := newTestManager(t)
	m.Config.PiCommand = "/bin/sh"
	m.Config.PiCommandArgs = []string{"-c", "echo boom >&2; exit 3", "sh"}
	_, err := m.AvailableModels()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want a wrapped *exec.ExitError with stderr detail, got %v", err)
	}
}

// launch_model reports what the current Pi was launched with; the bridge
// moving the mutable model must not change it, and a model the old bridge
// records while a launch is preparing is the one launched.
func TestLaunchModelIsSeparateAndRaceFree(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	setModel := func(model string) {
		t.Helper()
		if err := m.Store.WithAgentLock(a.ID, func() error {
			ag, err := m.Store.LoadAgent(a.ID)
			if err != nil {
				return err
			}
			ag.Model = model
			return m.Store.SaveAgent(ag)
		}); err != nil {
			t.Fatal(err)
		}
	}
	setModel("devx-faux/faux-1")
	// Change the model after the script is first written (when bridge
	// source is written) but before the nonce is stamped.
	m.bridgeSrc = []byte("// bridge")
	m.Tmux = Tmux{Exec: func(args ...string) (string, error) {
		return "", errors.New("tmux disabled")
	}}
	origCreator := m.Creator
	m.Creator = ensureHook{SessionCreator: origCreator, hook: func() { setModel("devx-faux/faux-2") }}
	_ = m.launch(a.ID, false) // fails at tmux after stamping
	ag, _ := m.Store.LoadAgent(a.ID)
	script, _ := os.ReadFile(filepath.Join(m.Store.AgentDir(a.ID), "launch.sh"))
	if ag.LaunchModel != "devx-faux/faux-2" || !strings.Contains(string(script), "'devx-faux/faux-2'") {
		t.Fatalf("launch must use the model read under the nonce lock: launch_model=%q\n%s", ag.LaunchModel, script)
	}
	// The bridge moving the live model leaves launch_model alone.
	setModel("openai/gpt-5")
	v, _ := m.AgentStatus(a.ID)
	if got := agentSummary(v)["launch_model"]; got != "devx-faux/faux-2" {
		t.Fatalf("launch_model must not follow the mutable model: %v", got)
	}
}

// ensureHook runs hook when EnsureTmux is called, i.e. after launchLocked
// read the agent but before the nonce is stamped (a model change the old
// bridge records in that window).
type ensureHook struct {
	SessionCreator
	hook func()
}

func (e ensureHook) EnsureTmux(name string, a *Agent) error {
	e.hook()
	return nil
}

func TestModelSyntaxRejectsFlagsAndCommands(t *testing.T) {
	m, _ := newTestManager(t)
	calls := withModels(m, append([]string{"-x/--evil"}, testModels...), nil)
	for _, bad := range []string{"--help", "-x/--evil", "/model gpt-5", "a b", "a\nb", strings.Repeat("x", MaxModelRefBytes+1)} {
		if _, err := m.ResolveModel(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
	if *calls != 0 {
		t.Fatal("syntax errors must not consult Pi")
	}
}

func TestStartWithInvalidModelStartsNothing(t *testing.T) {
	m, creator := newTestManager(t)
	withModels(m, testModels, nil)
	for _, model := range []string{"anthropic/claude-nope", "gpt-5", "/model anthropic/claude-sonnet-4-5"} {
		_, err := m.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k-" + model, Model: model})
		if err == nil {
			t.Fatalf("%q: start must fail", model)
		}
	}
	agents, _ := m.Store.ListAgents()
	if len(agents) != 0 || creator.creates != 0 {
		t.Fatalf("an invalid model must create nothing: agents=%d sessions=%d", len(agents), creator.creates)
	}
	// Catalog failure fails closed too.
	m2, creator2 := newTestManager(t)
	withModels(m2, nil, errors.New("pi not found"))
	_, err := m2.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k", Model: "anthropic/claude-sonnet-4-5"})
	var ce *ModelCatalogError
	if !errors.As(err, &ce) || creator2.creates != 0 {
		t.Fatalf("want ModelCatalogError and nothing created, got %v (creates=%d)", err, creator2.creates)
	}
}

func TestStartWithoutModelUsesPiDefault(t *testing.T) {
	m, _ := newTestManager(t)
	calls := withModels(m, nil, errors.New("must not be consulted"))
	_, _ = m.Start(StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k"})
	agents, _ := m.Store.ListAgents()
	if len(agents) != 1 || agents[0].Model != "" {
		t.Fatalf("default start must record no model: %+v", agents)
	}
	if *calls != 0 {
		t.Fatal("a start without model must not list models")
	}
	if script := m.launchScript(agents[0], "n", "/b.ts"); strings.Contains(script, "--model") {
		t.Fatalf("default launch must not pass --model:\n%s", script)
	}
}

func TestMCPStartModelErrorsAndSchema(t *testing.T) {
	m, _ := newTestManager(t)
	withModels(m, testModels, nil)
	s := &MCPServer{M: m, Name: "t", Version: "1"}
	var startSchema, sendSchema map[string]any
	for _, tl := range toolList() {
		props := tl.InputSchema["properties"].(map[string]any)
		switch tl.Name {
		case "pi_start_task":
			startSchema = props
			if !strings.Contains(tl.Description, "/model") || !strings.Contains(tl.Description, "invalid_model") {
				t.Fatalf("pi_start_task description must steer callers to model: %s", tl.Description)
			}
		case "pi_send":
			sendSchema = props
			if !strings.Contains(tl.Description, "NOT executed") {
				t.Fatalf("pi_send description must say slash commands are not executed: %s", tl.Description)
			}
		}
	}
	if startSchema["model"] == nil || sendSchema["model"] == nil {
		t.Fatal("pi_start_task and pi_send must accept model")
	}
	res := mcpCallRaw(t, s, "pi_start_task", map[string]any{"project": "proj", "prompt": "p", "idempotency_key": "k", "model": "anthropic/claude-nope"})
	sc := res["structuredContent"].(map[string]any)
	if res["isError"] != true || sc["error"] != "invalid_model" || sc["started"] != false {
		t.Fatalf("want invalid_model error: %+v", sc)
	}
	valid, _ := sc["valid_models"].([]any)
	if len(valid) != len(testModels) {
		t.Fatalf("valid_models must list the catalog: %+v", sc)
	}
	if agents, _ := m.Store.ListAgents(); len(agents) != 0 {
		t.Fatal("invalid model started an agent")
	}
}

func TestSendModelSwitchQueuedAndGated(t *testing.T) {
	m, _ := newTestManager(t)
	withModels(m, testModels, nil)
	a, _ := runningTaskWithBridge(t, m, BridgeModelProtocol-1)
	// An older bridge would ignore the switch: refuse, record nothing.
	_, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "next", IdempotencyKey: "s1", Model: "openai/gpt-5"})
	var se *ModelSwitchUnsupportedError
	if !errors.As(err, &se) || se.Protocol != BridgeModelProtocol-1 {
		t.Fatalf("want model_switch_unsupported, got %v", err)
	}
	tasks, _ := m.Store.ListTasks(a.ID)
	if len(tasks) != 1 {
		t.Fatalf("a refused switch must queue nothing: %d tasks", len(tasks))
	}
	// Retrying the same key after the refusal is not poisoned.
	liveBridge(t, m, a.ID, BridgeProtocol)
	r, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "next", IdempotencyKey: "s1", Model: "openai/gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := m.Store.LoadTask(a.ID, r.TaskID)
	if tk.Model != "openai/gpt-5" || r.Model != "openai/gpt-5" {
		t.Fatalf("task must carry the switch: %+v %+v", tk, r)
	}
	// Invalid model: nothing queued.
	if _, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "x", IdempotencyKey: "s2", Model: "nope/nope"}); err == nil {
		t.Fatal("invalid model must fail")
	}
	// Plain sends still work and carry no switch.
	r2, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "plain", IdempotencyKey: "s3"})
	if err != nil {
		t.Fatal(err)
	}
	if tk, _ := m.Store.LoadTask(a.ID, r2.TaskID); tk.Model != "" {
		t.Fatalf("plain send must not switch: %+v", tk)
	}
	if tasks, _ := m.Store.ListTasks(a.ID); len(tasks) != 3 {
		t.Fatalf("want 3 tasks, got %d", len(tasks))
	}
	// A supersede keeps the switch.
	qv, _ := m.Queue(a.ID, 0)
	var rev int64
	for _, it := range qv.Pending {
		if it.TaskID == r.TaskID {
			rev = it.Revision
			if it.RequestedModel != "openai/gpt-5" {
				t.Fatalf("pi_queue must show requested_model: %+v", it)
			}
		}
	}
	er, err := m.SupersedePending(SupersedePendingRequest{AgentID: a.ID, TaskID: r.TaskID, Prompt: "next v2", ExpectedRevision: rev, IdempotencyKey: "sp"})
	if err != nil {
		t.Fatal(err)
	}
	if nt, _ := m.Store.LoadTask(a.ID, er.ReplacementTaskID); nt.Model != "openai/gpt-5" {
		t.Fatalf("replacement lost the model switch: %+v", nt)
	}
}

func TestModelReportedInStatusListAndResult(t *testing.T) {
	m, _ := newTestManager(t)
	a, tk := runningTaskWithBridge(t, m, BridgeProtocol)
	s := &MCPServer{M: m, Name: "t", Version: "1"}
	if err := m.Store.WithAgentLock(a.ID, func() error {
		ag, err := m.Store.LoadAgent(a.ID)
		if err != nil {
			return err
		}
		ag.Model = "anthropic/claude-sonnet-4-5"
		ag.LaunchModel = "anthropic/claude-sonnet-4-5"
		if err := m.Store.SaveAgent(ag); err != nil {
			return err
		}
		b, err := m.Store.LoadBridge(a.ID)
		if err != nil {
			return err
		}
		b.Model, b.Heartbeat = "anthropic/claude-sonnet-4-5", time.Now()
		if err := writeJSONAtomic(m.Store.bridgePath(a.ID), b); err != nil {
			return err
		}
		t2, err := m.Store.LoadTask(a.ID, tk.ID)
		if err != nil {
			return err
		}
		t2.AcceptedModel = "anthropic/claude-sonnet-4-5"
		return m.Store.SaveTask(t2)
	}); err != nil {
		t.Fatal(err)
	}
	st := mcpCall(t, s, "pi_status", map[string]any{"task_id": tk.ID})
	ag := st["agent"].(map[string]any)
	task := st["task"].(map[string]any)
	if ag["model"] != "anthropic/claude-sonnet-4-5" || ag["launch_model"] != "anthropic/claude-sonnet-4-5" || task["model"] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("pi_status model: agent=%v task=%v", ag, task)
	}
	ls := mcpCall(t, s, "pi_list_sessions", map[string]any{})
	if got := ls["agents"].([]any)[0].(map[string]any)["model"]; got != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("pi_list_sessions model: %v", got)
	}
	if err := os.WriteFile(filepath.Join(m.Store.AgentDir(a.ID), "results", tk.ID+".txt"), []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := mcpCall(t, s, "pi_result", map[string]any{"task_id": tk.ID}); res["model"] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("pi_result model: %v", res)
	}
	// A heartbeat from an earlier launch does not vouch for the current Pi.
	if err := m.Store.WithAgentLock(a.ID, func() error {
		ag, _ := m.Store.LoadAgent(a.ID)
		ag.LaunchNonce = "newer"
		return m.Store.SaveAgent(ag)
	}); err != nil {
		t.Fatal(err)
	}
	if v, _ := m.AgentStatus(a.ID); v.Model != "" {
		t.Fatalf("stale bridge model must not be reported: %q", v.Model)
	}
}
