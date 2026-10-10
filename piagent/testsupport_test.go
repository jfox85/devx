package piagent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/internal/tmuxfixture"
)

// dirCreator is a SessionCreator over a temp directory and an isolated tmux
// socket: it never touches the user's DevX metadata or tmux server.
type dirCreator struct {
	base     string
	fx       *tmuxfixture.Fixture
	existing map[string]time.Time
	owner    map[string]string // session -> owning agent id
	creates  int
	instance string // instance id reported for created sessions (optional)
}

func (c *dirCreator) Create(name, project, agentID string) (CreatedSession, error) {
	if c.owner == nil {
		c.owner = map[string]string{}
	}
	if _, ok := c.existing[name]; ok && c.owner[name] != agentID {
		return CreatedSession{}, Denied("session %q is not owned by agent %s", name, agentID)
	}
	p := filepath.Join(c.base, name)
	if err := os.MkdirAll(p, 0o700); err != nil {
		return CreatedSession{}, err
	}
	if _, ok := c.existing[name]; !ok {
		c.creates++
		c.existing[name] = time.Now()
		c.owner[name] = agentID
	}
	return CreatedSession{Name: name, Path: p, Project: project, TmuxName: name, InstanceID: c.instance, CreatedAt: c.existing[name]}, nil
}

func (c *dirCreator) EnsureTmux(name, agentID string) error {
	if c.owner[name] != agentID {
		return Denied("session %q is not owned by agent %s", name, agentID)
	}
	if c.fx == nil {
		return fmt.Errorf("no tmux fixture configured")
	}
	if c.fx.Owns(name) {
		return nil
	}
	// NewSession records ownership before tmux runs.
	return c.fx.NewSession(name, filepath.Join(c.base, name), "sleep 86400")
}

func (c *dirCreator) Exists(name string) (bool, error) {
	_, ok := c.existing[name]
	return ok, nil
}

func newTestManager(t *testing.T) (*Manager, *dirCreator) {
	t.Helper()
	root := t.TempDir()
	creator := &dirCreator{base: t.TempDir(), existing: map[string]time.Time{}}
	// Unit tests must not reach any tmux server: every call fails.
	noTmux := Tmux{Exec: func(args ...string) (string, error) {
		return "", fmt.Errorf("tmux disabled in unit tests (%s)", strings.Join(args, " "))
	}}
	m := NewManager(NewStore(filepath.Join(root, "state")), noTmux, creator, Config{AllowedProjects: []string{"proj"}})
	return m, creator
}

// --- real interactive Pi fixture ----------------------------------------

type piFixture struct {
	t       *testing.T
	m       *Manager
	creator *dirCreator
	tmux    Tmux
	fx      *tmuxfixture.Fixture
}

// newPiFixture launches real interactive Pi TUIs (faux model, isolated Pi
// config dir, isolated tmux server). Skipped when pi/tmux are unavailable or
// DEVX_PI_E2E=0.
func newPiFixture(t *testing.T, extraEnv ...string) *piFixture {
	t.Helper()
	if os.Getenv("DEVX_PI_E2E") == "0" {
		t.Skip("DEVX_PI_E2E=0")
	}
	piBin := os.Getenv("DEVX_PI_BIN")
	if piBin == "" {
		var err error
		if piBin, err = exec.LookPath("pi"); err != nil {
			t.Skip("pi not installed")
		}
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// Private tmux server with a fixture-owned socket. Every tmux command
	// in this test (manager, bridge launch, human keystrokes) goes through
	// the guard; cleanup kills only sessions the fixture recorded, never a
	// server.
	fx, err := tmuxfixture.New(tmuxfixture.Options{})
	if err != nil {
		t.Fatal(err)
	}
	tm := Tmux{Exec: fx.Run}
	f := &piFixture{t: t, fx: fx}
	t.Cleanup(func() {
		// Cleanup kills only sessions this fixture recorded (exact =name),
		// never a server; the audit then covers cleanup too.
		if _, err := fx.Cleanup(); err != nil {
			t.Errorf("fixture cleanup: %v", err)
		}
		f.assertFixtureAudit()
		_ = os.RemoveAll(fx.Dir())
	})

	// Short paths: macOS limits socket/path lengths and Pi derives some.
	tmpRoot, err := os.MkdirTemp("/tmp", "dxpa-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("fixture state kept at %s", tmpRoot)
			return
		}
		_ = os.RemoveAll(tmpRoot)
	})
	piDir := filepath.Join(tmpRoot, "pi-agent-dir")
	_ = os.MkdirAll(piDir, 0o700)
	faux, _ := filepath.Abs("testdata/faux-model.ts")
	creator := &dirCreator{base: filepath.Join(tmpRoot, "wt"), fx: fx, existing: map[string]time.Time{}}
	m := NewManager(NewStore(filepath.Join(tmpRoot, "state")), tm, creator, Config{
		PiArgs: []string{
			"--offline", "--no-extensions", "--no-skills", "--no-context-files", "--no-mcp", "--no-approve",
			"--no-prompt-templates", "-e", faux, "--model", "devx-faux/faux-1",
			"--session-dir", filepath.Join(tmpRoot, "pi-sessions"),
		},
		AllowedProjects: []string{"proj"},
	})
	// "env VAR=... pi" keeps the launch script generic while isolating Pi's
	// config dir (no user extensions, auth, or settings are loaded).
	m.Config.PiCommand = "/usr/bin/env"
	m.Config.PiCommandArgs = []string{"PI_CODING_AGENT_DIR=" + piDir, "GATEPOST_HOST_DISABLE=1"}
	m.Config.PiCommandArgs = append(append(m.Config.PiCommandArgs, extraEnv...), piBin)
	f.m, f.creator, f.tmux = m, creator, tm
	return f
}

func (f *piFixture) start(prompt, key string) *StartResult {
	f.t.Helper()
	r, err := f.m.Start(StartRequest{Project: "proj", Prompt: prompt, IdempotencyKey: key})
	if err != nil {
		f.t.Fatalf("start: %v", err)
	}
	return r
}

func (f *piFixture) send(agentID, prompt, key string) string {
	f.t.Helper()
	r, err := f.m.Send(SendRequest{AgentID: agentID, Prompt: prompt, IdempotencyKey: key})
	if err != nil {
		f.t.Fatalf("send: %v", err)
	}
	return r.TaskID
}

func (f *piFixture) waitTask(taskID string, timeout time.Duration, states ...string) *TaskView {
	f.t.Helper()
	deadline := time.Now().Add(timeout)
	var tv *TaskView
	for time.Now().Before(deadline) {
		var err error
		tv, _, err = f.m.TaskStatus(taskID)
		if err != nil {
			f.t.Fatalf("status: %v", err)
		}
		for _, s := range states {
			if tv.State == s {
				return tv
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	f.t.Fatalf("task %s did not reach %v within %s; last=%+v\npane:\n%s", taskID, states, timeout, tv, f.pane(tv.AgentID))
	return nil
}

func (f *piFixture) waitAgent(agentID string, timeout time.Duration, pred func(*AgentView) bool, what string) *AgentView {
	f.t.Helper()
	deadline := time.Now().Add(timeout)
	var v *AgentView
	for time.Now().Before(deadline) {
		var err error
		if v, err = f.m.AgentStatus(agentID); err != nil {
			f.t.Fatalf("agent status: %v", err)
		}
		if pred(v) {
			return v
		}
		time.Sleep(100 * time.Millisecond)
	}
	f.t.Fatalf("agent %s: %s not reached in %s; state=%s detail=%s\npane:\n%s", agentID, what, timeout, v.State, v.Detail, f.pane(agentID))
	return nil
}

func (f *piFixture) online(agentID string) *AgentView {
	return f.waitAgent(agentID, 30*time.Second, func(v *AgentView) bool { return v.BridgeOnline }, "bridge online")
}

func (f *piFixture) pane(agentID string) string {
	out, err := f.m.Inspect(agentID, 60)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return out
}

// humanType types into the agent's pane exactly as a person at the keyboard
// would (literal keys, then Enter).
func (f *piFixture) humanType(agentID, text string, submit bool) {
	f.t.Helper()
	a, err := f.m.Store.LoadAgent(agentID)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.tmux.Run("send-keys", "-t", a.Binding.PaneID, "-l", text); err != nil {
		f.t.Fatal(err)
	}
	if submit {
		time.Sleep(150 * time.Millisecond)
		if _, err := f.tmux.Run("send-keys", "-t", a.Binding.PaneID, "Enter"); err != nil {
			f.t.Fatal(err)
		}
	}
}

func (f *piFixture) events(agentID string) []Event {
	evs, _, err := f.m.Store.ReadEvents(agentID, 0, MaxEventsPage)
	if err != nil {
		f.t.Fatal(err)
	}
	return evs
}

func eventTypes(evs []Event) string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Type)
	}
	return strings.Join(s, ",")
}

// assertFixtureAudit checks that every tmux argv this fixture executed was
// pinned to its own socket and that kill-server never ran.
func (f *piFixture) assertFixtureAudit() {
	f.t.Helper()
	data, err := os.ReadFile(f.fx.CommandLogPath())
	if err != nil {
		f.t.Fatalf("fixture audit log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	prefix := `["-S",` + strconvQuote(f.fx.Socket()) + `,`
	for _, l := range lines {
		if !strings.HasPrefix(l, prefix) || strings.Contains(l, `"kill-server"`) {
			f.t.Fatalf("unsafe tmux argv in audit: %s", l)
		}
	}
	if log := os.Getenv("DEVX_PIAGENT_AUDIT_DIR"); log != "" {
		_ = os.WriteFile(filepath.Join(log, f.t.Name()+".audit.log"), append([]byte("# socket "+f.fx.Socket()+"\n"), data...), 0o600)
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
