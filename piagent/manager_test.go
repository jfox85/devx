package piagent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Unit tests: no Pi, no tmux server. Agents are seeded directly so they
// exercise the store/manager contract; Pi-side behavior is covered by the
// E2E tests in takeover_e2e_test.go.

func seedAgent(t *testing.T, m *Manager) *Agent {
	t.Helper()
	a := &Agent{ID: newAgentID(), DevxSession: "s1", Worktree: t.TempDir(), PiSessionID: newPiSessionID(time.Now()),
		Lease: Lease{Holder: LeaseManaged, Generation: 1}, CreatedAt: time.Now()}
	if err := m.Store.ensureAgentDirs(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Store.WithAgentLock(a.ID, func() error { return m.Store.SaveAgent(a) }); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSendIsIdempotentAcrossRetries(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	r1, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "do x", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	// Retried after a dropped response: same task, nothing new queued.
	r2, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "do x", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if r1.TaskID != r2.TaskID || r1.Replayed || !r2.Replayed {
		t.Fatalf("r1=%+v r2=%+v", r1, r2)
	}
	tasks, _ := m.Store.ListTasks(a.ID)
	if len(tasks) != 1 {
		t.Fatalf("tasks=%d", len(tasks))
	}
	// Same key, different prompt: rejected rather than silently aliased.
	if _, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "do y", IdempotencyKey: "k"}); err == nil {
		t.Fatal("expected conflict")
	}
	// Key is required.
	if _, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "do y"}); err == nil {
		t.Fatal("expected missing key error")
	}
}

// A crash after the idempotency record is written but before the task is
// enqueued must be completed by the retry, with the same task id.
func TestSendRetryCompletesInterruptedEnqueue(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	keyHash := hashKey("send", a.ID, "k")
	rec := &idemRecord{Kind: "send", KeyHash: keyHash, RequestHash: hashKey("p"), AgentID: a.ID, TaskID: newTaskID(), CreatedAt: time.Now()}
	if err := m.Store.saveIdem(rec); err != nil {
		t.Fatal(err)
	}
	r, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if r.TaskID != rec.TaskID {
		t.Fatalf("task id changed: %s != %s", r.TaskID, rec.TaskID)
	}
	if _, err := m.Store.LoadTask(a.ID, rec.TaskID); err != nil {
		t.Fatalf("task not enqueued: %v", err)
	}
}

func TestConcurrentSendsGetDistinctOrderedTasks(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	const n = 24
	var wg sync.WaitGroup
	ids := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Half the goroutines retry the same key concurrently.
			key := fmt.Sprintf("k%d", i/2)
			r, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "p" + key, IdempotencyKey: key})
			errs[i] = err
			if err == nil {
				ids[i] = r.TaskID
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}
	for i := 0; i < n; i += 2 {
		if ids[i] != ids[i+1] {
			t.Fatalf("same key got different tasks: %s %s", ids[i], ids[i+1])
		}
	}
	tasks, _ := m.Store.ListTasks(a.ID)
	if len(tasks) != n/2 {
		t.Fatalf("tasks=%d want %d", len(tasks), n/2)
	}
	seen := map[int64]bool{}
	for _, tk := range tasks {
		if seen[tk.Order] {
			t.Fatalf("duplicate order %d", tk.Order)
		}
		seen[tk.Order] = true
	}
	evs, last, _ := m.Store.ReadEvents(a.ID, 0, MaxEventsPage)
	if int64(len(evs)) != last || last != n/2 {
		t.Fatalf("events=%d last=%d", len(evs), last)
	}
	for i, e := range evs {
		if e.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d: %d", i, e.Seq)
		}
	}
}

func TestStartIdempotentAndPermissionDenied(t *testing.T) {
	m, creator := newTestManager(t)
	// Disallowed project.
	_, err := m.Start(StartRequest{Project: "other", Prompt: "p", IdempotencyKey: "k"})
	if !IsPermissionDenied(err) {
		t.Fatalf("want permission denied, got %v", err)
	}
	// Existing session is never adopted.
	creator.existing["taken"] = time.Now().Add(-time.Hour)
	_, err = m.Start(StartRequest{Project: "proj", SessionName: "taken", Prompt: "p", IdempotencyKey: "k2"})
	if !IsPermissionDenied(err) {
		t.Fatalf("want permission denied for existing session, got %v", err)
	}
	if creator.creates != 0 {
		t.Fatalf("creates=%d", creator.creates)
	}
}

// Start crashes after creating the session/agent but before launching Pi
// (simulated by tmux being unavailable on the first attempt). The retry with
// the same key resumes the same agent/task/session; the session is created
// exactly once.
func TestStartRetryAfterPartialFailureResumes(t *testing.T) {
	m, creator := newTestManager(t)
	failing := &failingTmuxCreator{dirCreator: creator}
	m.Creator = failing
	req := StartRequest{Project: "proj", Prompt: "p", IdempotencyKey: "k"}
	if _, err := m.Start(req); err == nil {
		t.Fatal("expected launch failure")
	}
	agents, _ := m.Store.ListAgents()
	if len(agents) != 1 {
		t.Fatalf("agents=%d", len(agents))
	}
	first := agents[0]
	v, _ := m.AgentStatus(first.ID)
	if v.State != AgentNotLaunched {
		t.Fatalf("state=%s", v.State)
	}
	if _, err := m.Start(req); err == nil {
		t.Fatal("expected launch failure again")
	}
	agents, _ = m.Store.ListAgents()
	if len(agents) != 1 || agents[0].ID != first.ID || creator.creates != 1 {
		t.Fatalf("agents=%d creates=%d", len(agents), creator.creates)
	}
	tasks, _ := m.Store.ListTasks(first.ID)
	if len(tasks) != 1 || tasks[0].ID != first.StartTaskID {
		t.Fatalf("tasks=%+v", tasks)
	}
}

type failingTmuxCreator struct{ *dirCreator }

func (f *failingTmuxCreator) EnsureTmux(string, *Agent) error { return errors.New("tmux unavailable") }

func TestCancelSemantics(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	r, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k"})
	if _, err := m.Cancel(r.TaskID); err != nil {
		t.Fatal(err)
	}
	tk, _ := m.Store.LoadTask(a.ID, r.TaskID)
	if tk.State != TaskCancelled {
		t.Fatalf("state=%s", tk.State)
	}
	// Cancel is idempotent on terminal tasks.
	if _, err := m.Cancel(r.TaskID); err != nil {
		t.Fatal(err)
	}
	// Running task under human control: denied.
	r2, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "p2", IdempotencyKey: "k2"})
	_ = m.Store.WithAgentLock(a.ID, func() error {
		tk, _ := m.Store.LoadTask(a.ID, r2.TaskID)
		tk.State = TaskRunning
		return m.Store.SaveTask(tk)
	})
	if _, err := m.Takeover(a.ID, "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel(r2.TaskID); !IsPermissionDenied(err) {
		t.Fatalf("want permission denied, got %v", err)
	}
	if _, err := m.Release(a.ID, "h", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Cancel(r2.TaskID); err != nil {
		t.Fatal(err)
	}
	tk, _ = m.Store.LoadTask(a.ID, r2.TaskID)
	if !tk.CancelRequested || tk.State != TaskRunning {
		t.Fatalf("running cancel should request abort: %+v", tk)
	}
}

func TestLeaseGenerationsAndReleaseDrop(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	r, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k"})
	got, _ := m.Takeover(a.ID, "h")
	if got.Lease.Holder != LeaseHuman || got.Lease.Generation != 2 {
		t.Fatalf("%+v", got.Lease)
	}
	again, _ := m.Takeover(a.ID, "h")
	if again.Lease.Generation != 2 {
		t.Fatal("repeated takeover must not bump generation")
	}
	rel, _ := m.Release(a.ID, "h", true)
	if rel.Lease.Holder != LeaseManaged || rel.Lease.Generation != 3 {
		t.Fatalf("%+v", rel.Lease)
	}
	tk, _ := m.Store.LoadTask(a.ID, r.TaskID)
	if tk.State != TaskCancelled {
		t.Fatalf("release --drop-queued should cancel waiting: %s", tk.State)
	}
}

// After an MCP server restart nothing is in memory: a running task whose Pi
// process is gone (stopped bridge, dead PID) must reconcile to unknown, and a
// waiting task must stay waiting with an explanation.
func TestRestartReconciliation(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	_ = m.Store.WithAgentLock(a.ID, func() error {
		a, _ := m.Store.LoadAgent(a.ID)
		a.Binding = Binding{TmuxSession: "s1", WindowID: "@1", PaneID: "%999999"}
		a.LaunchNonce = "n"
		return m.Store.SaveAgent(a)
	})
	run, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k1"})
	wait, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "q", IdempotencyKey: "k2"})
	_ = m.Store.WithAgentLock(a.ID, func() error {
		tk, _ := m.Store.LoadTask(a.ID, run.TaskID)
		tk.State, tk.BridgeInstance = TaskRunning, "inst"
		return m.Store.SaveTask(tk)
	})
	_ = writeJSONAtomic(m.Store.bridgePath(a.ID), Bridge{Instance: "inst", Nonce: "n", PID: 999999, Pane: "%999999", Heartbeat: time.Now().Add(-time.Minute)})

	// "Restart": a brand-new manager over the same root. tmux confirms the
	// pane is gone (a failed query would only make the state unknown).
	gone := Tmux{Exec: func(args ...string) (string, error) {
		return "", fmt.Errorf("tmux %s: exit status 1: can't find pane: %%999999", args[0])
	}}
	m2 := NewManager(NewStore(m.Store.Root), gone, m.Creator, m.Config)
	tv, av, err := m2.TaskStatus(run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if tv.State != TaskUnknown || tv.Note == "" {
		t.Fatalf("running task after restart: %+v", tv)
	}
	if av.State != AgentPaneExited {
		t.Fatalf("agent state=%s", av.State)
	}
	tw, _, _ := m2.TaskStatus(wait.TaskID)
	if tw.State != TaskWaiting || tw.WaitingReason != WaitBindingStale {
		t.Fatalf("waiting task after restart: %+v", tw)
	}
}

func TestEventsCursorReplayAndWait(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	for i := 0; i < 5; i++ {
		_, _ = m.Send(SendRequest{AgentID: a.ID, Prompt: fmt.Sprintf("p%d", i), IdempotencyKey: fmt.Sprintf("k%d", i)})
	}
	// Client processed up to seq 2, then its connection dropped. Reading
	// again from 2 replays exactly 3..5, any number of times.
	for round := 0; round < 2; round++ {
		evs, last, err := m.Events(a.ID, 2, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 3 || evs[0].Seq != 3 || last != 5 {
			t.Fatalf("round %d: %d events, first=%d last=%d", round, len(evs), evs[0].Seq, last)
		}
	}
	// Page limit.
	evs, _, _ := m.Events(a.ID, 0, 2, 0)
	if len(evs) != 2 {
		t.Fatalf("limit: %d", len(evs))
	}
	// Torn trailing line from a crashed writer is ignored, not fatal.
	f := m.Store.eventsPath(a.ID)
	appendRaw(t, f, `{"seq":6,"type":"tor`)
	if evs, _, err := m.Events(a.ID, 5, 0, 0); err != nil || len(evs) != 0 {
		t.Fatalf("torn line: %v %d", err, len(evs))
	}
	// Wait blocks until an event lands (written by another goroutine).
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _ = m.Send(SendRequest{AgentID: a.ID, Prompt: "late", IdempotencyKey: "late"})
	}()
	start := time.Now()
	evs, _, err := m.Events(a.ID, 5, 0, 5*time.Second)
	if err != nil || len(evs) != 1 || evs[0].Type != "task_queued" {
		t.Fatalf("wait: %v %+v", err, evs)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("wait did not return promptly")
	}
}

func TestBoundsAndValidation(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	if _, err := m.Send(SendRequest{AgentID: a.ID, Prompt: strings.Repeat("x", MaxPromptBytes+1), IdempotencyKey: "k"}); err == nil {
		t.Fatal("oversized prompt accepted")
	}
	if _, err := m.Send(SendRequest{AgentID: "../../etc", Prompt: "p", IdempotencyKey: "k"}); err == nil {
		t.Fatal("bad agent id accepted")
	}
	if _, _, err := m.TaskStatus("pt_../x"); err == nil {
		t.Fatal("bad task id accepted")
	}
	r, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k"})
	text := strings.Repeat("é", 20000) // multi-byte, 40000 bytes
	if err := writeFileAtomic(m.Store.resultPath(a.ID, r.TaskID), []byte(text)); err != nil {
		t.Fatal(err)
	}
	tv, _, _ := m.TaskStatus(r.TaskID)
	if len(tv.Excerpt) > MaxExcerptBytes || !tv.ExcerptTrunc {
		t.Fatalf("excerpt %d", len(tv.Excerpt))
	}
	var got strings.Builder
	for off := 1; ; { // deliberately misaligned start
		c, err := m.Result(r.TaskID, off, 1001)
		if err != nil {
			t.Fatal(err)
		}
		if len(c.Text) > 1001 || !strings.HasPrefix(text[c.Offset:], c.Text) {
			t.Fatal("chunk not a valid slice")
		}
		got.WriteString(c.Text)
		off = c.NextOffset
		if c.EOF {
			break
		}
	}
	if got.String() != text {
		t.Fatal("chunks did not reassemble (start offset should snap to rune boundary 0)")
	}
	if _, err := m.Result(r.TaskID, len(text)+1, 0); err == nil {
		t.Fatal("out-of-range offset accepted")
	}
}

func TestRedact(t *testing.T) {
	cases := []string{
		"key sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
		"OPENAI_API_KEY=sk-proj-abcdefghijklmnop12345",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123",
		"Authorization: Bearer abcdefghijklmnopqrstuvwx",
		"AWS AKIAABCDEFGHIJKLMNOP",
		"password: hunter2hunter2",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----",
	}
	for _, c := range cases {
		out := Redact(c)
		if !strings.Contains(out, "REDACTED") {
			t.Errorf("not redacted: %q -> %q", c, out)
		}
	}
	if Redact("ordinary text, 42 tests passed") != "ordinary text, 42 tests passed" {
		t.Error("false positive")
	}
}

func appendRaw(t *testing.T, path, s string) {
	t.Helper()
	if err := appendFile(path, s); err != nil {
		t.Fatal(err)
	}
}

// --- MCP protocol ---------------------------------------------------------

func mcpRoundTrip(t *testing.T, s *MCPServer, reqs ...string) []map[string]any {
	t.Helper()
	pr, pw := io.Pipe()
	var out strings.Builder
	done := make(chan error)
	go func() { done <- s.Serve(pr, &out) }()
	for _, r := range reqs {
		_, _ = pw.Write([]byte(r + "\n"))
	}
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var resps []map[string]any
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("bad response %q", sc.Text())
		}
		resps = append(resps, m)
	}
	return resps
}

func byID(resps []map[string]any, id float64) map[string]any {
	for _, r := range resps {
		if r["id"] == id {
			return r
		}
	}
	return nil
}

func TestMCPToolsAndPermissionDenial(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	s := &MCPServer{M: m, Name: "devx-pi", Version: "test"}
	resps := mcpRoundTrip(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"shell_exec","arguments":{"cmd":"id"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"pi_start_task","arguments":{"project":"nope","prompt":"p","idempotency_key":"k"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"pi_send","arguments":{"agent_id":"`+a.ID+`","prompt":"hi","idempotency_key":"s1"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"pi_send","arguments":{"agent_id":"`+a.ID+`","prompt":"hi","idempotency_key":"s1"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"pi_send","arguments":{"agent_id":"`+a.ID+`","prompt":"hi","idempotency_key":"s1","extra":1}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"bogus"}`,
		`not json`,
	)
	init := byID(resps, 1)["result"].(map[string]any)
	if init["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("init: %+v", init)
	}
	tools := byID(resps, 2)["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"pi_list_sessions", "pi_start_task", "pi_send", "pi_status", "pi_result", "pi_events", "pi_cancel"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	for _, forbidden := range []string{"pi_takeover", "pi_release", "shell_exec", "exec"} {
		if names[forbidden] {
			t.Errorf("tool %s must not be exposed", forbidden)
		}
	}
	if len(names) != 7 {
		t.Errorf("unexpected tool count %d", len(names))
	}
	errKind := func(id float64) string {
		r := byID(resps, id)["result"].(map[string]any)
		if r["isError"] != true {
			return ""
		}
		return r["structuredContent"].(map[string]any)["error"].(string)
	}
	if errKind(3) != "permission_denied" || errKind(4) != "permission_denied" {
		t.Fatalf("permission denials: %q %q", errKind(3), errKind(4))
	}
	if errKind(7) != "failed" {
		t.Fatal("unknown arguments must be rejected")
	}
	s5 := byID(resps, 5)["result"].(map[string]any)["structuredContent"].(map[string]any)
	s6 := byID(resps, 6)["result"].(map[string]any)["structuredContent"].(map[string]any)
	if s5["task_id"] != s6["task_id"] {
		t.Fatal("retried MCP send created a new task")
	}
	// Tool calls run concurrently, so read events after the sends returned.
	resps2 := mcpRoundTrip(t, s, `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"pi_events","arguments":{"agent_id":"`+a.ID+`","after_seq":0}}}`)
	ev := byID(resps2, 8)["result"].(map[string]any)["structuredContent"].(map[string]any)
	if len(ev["events"].([]any)) != 1 {
		t.Fatalf("events: %+v", ev)
	}
	if byID(resps, 9)["error"] == nil {
		t.Fatal("unknown method should be a JSON-RPC error")
	}
}

func appendFile(path, s string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(s)
	return err
}

// An MCP server killed while holding a start/send idempotency lock must not
// block the client's retry until the stale timeout.
func TestIdempotencyLockFromDeadProcessIsReclaimed(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	keyHash := hashKey("send", a.ID, "k")
	lock := filepath.Join(m.Store.idemDir(), keyHash+".lock")
	if err := os.MkdirAll(lock, 0o700); err != nil {
		t.Fatal(err)
	}
	// A pid that is certainly not running.
	dead := 999999
	for processAlive(dead) {
		dead--
	}
	_ = os.WriteFile(filepath.Join(lock, "pid"), []byte(strconv.Itoa(dead)), 0o600)
	start := time.Now()
	if _, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("retry waited %s for a dead owner's lock", time.Since(start))
	}
	// A live owner (this process, simulated as another holder) still blocks.
	_ = os.MkdirAll(lock, 0o700)
	_ = os.WriteFile(filepath.Join(lock, "pid"), []byte(strconv.Itoa(os.Getppid())), 0o600)
	release, err := acquireOwnedDirLock(lock, 300*time.Millisecond, time.Hour)
	if err == nil {
		release()
		t.Fatal("lock held by a live process was taken")
	}
	_ = os.RemoveAll(lock)
}

// A start whose session name is already owned by a different agent (or by
// nobody) is a permission denial; the start's own IDs persist so a retry
// is still denied the same way and nothing is launched.
func TestStartDeniesForeignOwnedSession(t *testing.T) {
	m, creator := newTestManager(t)
	creator.existing["race"] = time.Now()
	creator.owner = map[string]string{"race": "pa_someone_else"}
	// Exists() is checked first; simulate the race where the session
	// appears between Exists and Create.
	m.Creator = &raceCreator{dirCreator: creator}
	req := StartRequest{Project: "proj", SessionName: "race", Prompt: "p", IdempotencyKey: "k"}
	for i := 0; i < 2; i++ {
		if _, err := m.Start(req); !IsPermissionDenied(err) {
			t.Fatalf("attempt %d: want permission denied, got %v", i, err)
		}
	}
	agents, _ := m.Store.ListAgents()
	if len(agents) != 0 {
		t.Fatalf("agent record created for a denied start: %d", len(agents))
	}
}

type raceCreator struct{ *dirCreator }

func (r *raceCreator) Exists(string) (bool, error) { return false, nil }

// Removing an agent's session retires it: waiting work is cancelled, new
// sends are denied, status says retired, and records stay readable.
func TestRetireForSessionStopsNewWork(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	r, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RetireForSession(a.ID, "other-session"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Store.LoadAgent(a.ID); got.RetiredAt != nil {
		t.Fatal("retired for a different session")
	}
	if err := m.RetireForSession(a.ID, a.DevxSession); err != nil {
		t.Fatal(err)
	}
	if tk, _ := m.Store.LoadTask(a.ID, r.TaskID); tk.State != TaskCancelled {
		t.Fatalf("waiting task state=%s", tk.State)
	}
	if _, err := m.Send(SendRequest{AgentID: a.ID, Prompt: "p2", IdempotencyKey: "k2"}); !IsPermissionDenied(err) {
		t.Fatalf("send to retired agent: %v", err)
	}
	if v, err := m.AgentStatus(a.ID); err != nil || v.State != AgentRetired {
		t.Fatalf("status=%v err=%v", v, err)
	}
	if err := m.RetireForSession(a.ID, a.DevxSession); err != nil {
		t.Fatalf("idempotent retire: %v", err)
	}
	if err := m.RetireForSession(newAgentID(), "x"); err != nil {
		t.Fatalf("missing agent: %v", err)
	}
}

// A crash between appending an event and updating the seq cache must not
// make the next event reuse that seq (and become invisible to readers).
func TestAppendEventRecoversSeqFromLog(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	for i := 0; i < 3; i++ {
		if _, err := m.Store.AppendEvent(a.ID, Event{Type: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate the crash: the cache lags the log.
	if err := os.WriteFile(m.Store.seqPath(a.ID), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev, err := m.Store.AppendEvent(a.ID, Event{Type: "after-crash"})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Seq != 4 {
		t.Fatalf("seq=%d want 4", ev.Seq)
	}
	evs, last, _ := m.Store.ReadEvents(a.ID, 3, 10)
	if len(evs) != 1 || evs[0].Type != "after-crash" || last != 4 {
		t.Fatalf("events after 3: %+v last=%d", evs, last)
	}
}

// CodeRabbit r4238379180: a transient tmux failure (server busy, timeout,
// tmux missing from PATH) is not proof the pane is gone. A running task must
// stay running; only a confirmed absence (or a dead bridge instance) may
// orphan it.
func TestTransientTmuxErrorDoesNotOrphanRunningTask(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	_ = m.Store.WithAgentLock(a.ID, func() error {
		a, _ := m.Store.LoadAgent(a.ID)
		a.Binding = Binding{TmuxSession: "s1", WindowID: "@1", PaneID: "%5"}
		a.LaunchNonce = "n"
		return m.Store.SaveAgent(a)
	})
	run, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k1"})
	_ = m.Store.WithAgentLock(a.ID, func() error {
		tk, _ := m.Store.LoadTask(a.ID, run.TaskID)
		tk.State, tk.BridgeInstance = TaskRunning, "inst"
		return m.Store.SaveTask(tk)
	})
	// Live bridge, same instance, fresh heartbeat.
	_ = writeJSONAtomic(m.Store.bridgePath(a.ID), Bridge{Instance: "inst", Nonce: "n", PID: os.Getpid(), Pane: "%5", Heartbeat: time.Now()})

	mode := "error"
	m.Tmux = Tmux{Exec: func(args ...string) (string, error) {
		switch mode {
		case "error":
			return "", fmt.Errorf("tmux display-message: exit status 1: server exited unexpectedly")
		case "absent":
			return "", fmt.Errorf("tmux display-message: exit status 1: can't find pane: %%5")
		}
		return "%5\ts1\t@1\t0\t1\ts1\tpi", nil
	}}
	tv, av, err := m.TaskStatus(run.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if tv.State != TaskRunning {
		t.Fatalf("transient tmux error must not terminalize the task: %+v", tv)
	}
	if av.State == AgentPaneExited {
		t.Fatalf("transient tmux error must not report the pane as gone: %+v", av)
	}
	mode = "ok"
	if tv, _, _ := m.TaskStatus(run.TaskID); tv.State != TaskRunning {
		t.Fatalf("after recovery: %+v", tv)
	}
	// A confirmed absence still orphans it once the bridge is no longer
	// heartbeating (a live same-instance heartbeat proves Pi is alive).
	mode = "absent"
	if tv, _, _ := m.TaskStatus(run.TaskID); tv.State != TaskRunning {
		t.Fatalf("live heartbeat must outweigh tmux: %+v", tv)
	}
	_ = writeJSONAtomic(m.Store.bridgePath(a.ID), Bridge{Instance: "inst", Nonce: "n", PID: os.Getpid(), Pane: "%5", Heartbeat: time.Now().Add(-time.Hour)})
	if tv, _, _ := m.TaskStatus(run.TaskID); tv.State != TaskUnknown {
		t.Fatalf("confirmed pane absence with a stale bridge must orphan: %+v", tv)
	}
}

// CodeRabbit r4238379183: a chunk always advances the cursor, even when
// max_bytes is smaller than the rune at offset.
func TestResultChunkAlwaysAdvances(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	r, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k1"})
	text := "é漢x"
	_ = m.Store.WithAgentLock(a.ID, func() error {
		tk, _ := m.Store.LoadTask(a.ID, r.TaskID)
		tk.State = TaskCompleted
		return m.Store.SaveTask(tk)
	})
	if err := os.WriteFile(m.Store.resultPath(a.ID, r.TaskID), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	var got string
	off := 0
	for i := 0; i < 10; i++ {
		c, err := m.Result(r.TaskID, off, 1)
		if err != nil {
			t.Fatal(err)
		}
		if c.NextOffset <= off {
			t.Fatalf("chunk at %d did not advance: %+v", off, c)
		}
		got += c.Text
		off = c.NextOffset
		if c.EOF {
			break
		}
	}
	if got != text {
		t.Fatalf("reassembled %q want %q", got, text)
	}
}

// A failed tmux query must make launch/relaunch fail closed: no second Pi
// window, no respawn, no orphaning, no state change.
func TestTmuxUnavailableFailsClosed(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	_ = m.Store.WithAgentLock(a.ID, func() error {
		a, _ := m.Store.LoadAgent(a.ID)
		a.Binding = Binding{TmuxSession: "s1", WindowID: "@1", PaneID: "%5"}
		return m.Store.SaveAgent(a)
	})
	m.Creator = okTmuxCreator{m.Creator}
	var calls []string
	m.Tmux = Tmux{Exec: func(args ...string) (string, error) {
		calls = append(calls, args[0])
		return "", fmt.Errorf("tmux %s: exit status 1: server exited unexpectedly", args[0])
	}}
	if err := m.launch(a.ID, false); !errors.Is(err, ErrTmuxUnavailable) {
		t.Fatalf("launch with tmux unreachable: want ErrTmuxUnavailable, got %v", err)
	}
	if err := m.launch(a.ID, true); !errors.Is(err, ErrTmuxUnavailable) {
		t.Fatalf("relaunch with tmux unreachable: want ErrTmuxUnavailable, got %v", err)
	}
	for _, c := range calls {
		if c != "display-message" {
			t.Fatalf("only read-only tmux queries may run, got %q", c)
		}
	}
	got, _ := m.Store.LoadAgent(a.ID)
	if got.LaunchCount != 0 || got.LaunchNonce != "" {
		t.Fatalf("nothing may change: %+v", got)
	}
}

// okTmuxCreator reports the session's tmux as present without running tmux.
type okTmuxCreator struct{ SessionCreator }

func (okTmuxCreator) EnsureTmux(string, *Agent) error { return nil }

// Review M2: a missing tmux server is a confirmed absence (panes cannot
// outlive their server), not a query failure; otherwise Start's resume could
// never relaunch.
func TestNoTmuxServerIsAbsenceNotFailure(t *testing.T) {
	for _, msg := range []string{
		"tmux display-message: exit status 1: no server running on /tmp/tmux-501/default",
		"tmux display-message: exit status 1: error connecting to /tmp/x/sock (No such file or directory)",
		"tmux display-message: exit status 1: can't find pane: %5",
	} {
		p := Tmux{Exec: func(...string) (string, error) { return "", errors.New(msg) }}.Pane("%5")
		if p.Exists || p.QueryFailed {
			t.Errorf("%q: want confirmed absence, got %+v", msg, p)
		}
	}
	for _, msg := range []string{
		"signal: killed",
		"tmux display-message: exit status 1: error connecting to /tmp/x/sock (Permission denied)",
		"tmux display-message: exit status 1: error connecting to /tmp/x/sock (File name too long)",
	} {
		p := Tmux{Exec: func(...string) (string, error) { return "", errors.New(msg) }}.Pane("%5")
		if !p.QueryFailed {
			t.Errorf("%q says nothing about the pane: must be QueryFailed, got %+v", msg, p)
		}
	}
}

// Review M1: launch queries the pane once. A tmux failure on a later call
// must not turn a relaunch into a second Pi window.
func TestRelaunchUsesSinglePaneAnswer(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	_ = m.Store.WithAgentLock(a.ID, func() error {
		a, _ := m.Store.LoadAgent(a.ID)
		a.Binding = Binding{TmuxSession: "s1", WindowID: "@1", PaneID: "%5"}
		return m.Store.SaveAgent(a)
	})
	m.Creator = okTmuxCreator{m.Creator}
	n := 0
	var cmds []string
	m.Tmux = Tmux{Exec: func(args ...string) (string, error) {
		cmds = append(cmds, args[0])
		if args[0] == "display-message" {
			n++
			if n == 1 {
				return "%5\ts1\t@1\t0\t1\ts1\tpi", nil
			}
			return "", errors.New("tmux display-message: exit status 1: server exited unexpectedly")
		}
		return "", nil
	}}
	_ = m.launch(a.ID, true)
	for _, c := range cmds {
		if c == "new-window" {
			t.Fatalf("relaunch opened a second window after a transient failure: %v", cmds)
		}
	}
	if n != 1 {
		t.Fatalf("pane queried %d times, want 1: %v", n, cmds)
	}
}

// Review L1: invalid UTF-8 never moves the cursor backwards or stalls.
func TestResultChunkAdvancesOnInvalidUTF8(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	r, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k1"})
	_ = m.Store.WithAgentLock(a.ID, func() error {
		tk, _ := m.Store.LoadTask(a.ID, r.TaskID)
		tk.State = TaskCompleted
		return m.Store.SaveTask(tk)
	})
	text := "a\x80\x80x"
	_ = os.WriteFile(m.Store.resultPath(a.ID, r.TaskID), []byte(text), 0o600)
	off := 1
	for i := 0; i < 10; i++ {
		c, err := m.Result(r.TaskID, off, 1)
		if err != nil {
			t.Fatal(err)
		}
		if c.NextOffset <= off {
			t.Fatalf("chunk at %d did not advance: %+v", off, c)
		}
		off = c.NextOffset
		if c.EOF {
			return
		}
	}
	t.Fatal("did not reach EOF")
}

// Review round 2: a confirmed-absent pane (e.g. a wrong socket in the MCP
// process) must not orphan a task while the same Pi instance is heartbeating;
// once the heartbeat is stale it may.
func TestLiveSameInstanceBridgeIsNeverOrphaned(t *testing.T) {
	m, _ := newTestManager(t)
	a := seedAgent(t, m)
	_ = m.Store.WithAgentLock(a.ID, func() error {
		a, _ := m.Store.LoadAgent(a.ID)
		a.Binding = Binding{TmuxSession: "s1", WindowID: "@1", PaneID: "%5"}
		a.LaunchNonce = "n"
		return m.Store.SaveAgent(a)
	})
	run, _ := m.Send(SendRequest{AgentID: a.ID, Prompt: "p", IdempotencyKey: "k1"})
	_ = m.Store.WithAgentLock(a.ID, func() error {
		tk, _ := m.Store.LoadTask(a.ID, run.TaskID)
		tk.State, tk.BridgeInstance = TaskRunning, "inst"
		return m.Store.SaveTask(tk)
	})
	m.Tmux = Tmux{Exec: func(args ...string) (string, error) {
		return "", fmt.Errorf("tmux display-message: exit status 1: error connecting to /tmp/wrong/default (No such file or directory)")
	}}
	_ = writeJSONAtomic(m.Store.bridgePath(a.ID), Bridge{Instance: "inst", Nonce: "n", PID: os.Getpid(), Pane: "%5", Heartbeat: time.Now()})
	if tv, _, _ := m.TaskStatus(run.TaskID); tv.State != TaskRunning {
		t.Fatalf("live same-instance bridge must keep the task running: %+v", tv)
	}
	// Heartbeat stale and its process gone: now it is orphaned.
	_ = writeJSONAtomic(m.Store.bridgePath(a.ID), Bridge{Instance: "inst", Nonce: "n", PID: 999999, Pane: "%5", Heartbeat: time.Now().Add(-time.Hour)})
	if tv, _, _ := m.TaskStatus(run.TaskID); tv.State != TaskUnknown {
		t.Fatalf("dead pane with stale bridge must orphan: %+v", tv)
	}
}

// processAlive must tell a running process from an exited one on every
// platform (Windows has no signal 0): stale-lock reclamation depends on it.
func TestProcessAliveDistinguishesExitedProcess(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("this process must be alive")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if processAlive(cmd.Process.Pid) {
		t.Fatalf("exited process %d reported alive", cmd.Process.Pid)
	}
	if processAlive(0) || processAlive(-1) {
		t.Fatal("non-positive pids are never alive")
	}
}
