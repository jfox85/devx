package tmuxfixture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// These tests never start tmux. They use a recording executor and assert
// what WOULD have been executed (argv + env), and that rejected commands
// never reach the executor.

type recorder struct {
	mu    sync.Mutex
	calls [][]string
	envs  [][]string
	// running simulates which sessions exist for has-session.
	running map[string]bool
	// noConfig counts calls that carried the fixed "-f /dev/null".
	noConfig int
}

func (r *recorder) exec(argv, env []string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Every executed argv is "-S <sock> -f /dev/null <cmd...>"; record it
	// without the fixed config flag (asserted in
	// TestFixtureNeverLoadsUserTmuxConfig) so indexes stay readable.
	if len(argv) >= 4 && argv[2] == "-f" && argv[3] == os.DevNull {
		r.noConfig++
		argv = append(append([]string(nil), argv[:2]...), argv[4:]...)
	}
	r.calls = append(r.calls, append([]string(nil), argv...))
	r.envs = append(r.envs, append([]string(nil), env...))
	if len(argv) >= 4 && argv[2] == "has-session" {
		if !r.running[strings.TrimPrefix(argv[4], "=")] {
			return "", fmt.Errorf("no session")
		}
	}
	if len(argv) >= 5 && argv[2] == "new-session" {
		r.running[argv[5]] = true
	}
	return "", nil
}

// hostileEnv simulates running inside a real tmux whose server we must
// never reach: TMUX points at the default socket and TMUX_TMPDIR at a
// directory a careless caller might believe isolates us.
func hostileEnv(t *testing.T) string {
	t.Helper()
	def := fmt.Sprintf("/private/tmp/tmux-%d/default", os.Getuid())
	t.Setenv("TMUX", def+",6528,62")
	t.Setenv("TMUX_PANE", "%244")
	t.Setenv("TMUX_TMPDIR", "/tmp/devx-mcp-e2e/tmux")
	return def
}

func newMocked(t *testing.T) (*Fixture, *recorder) {
	t.Helper()
	r := &recorder{running: map[string]bool{}}
	f, err := New(Options{BaseDir: t.TempDir(), Exec: r.exec})
	if err != nil {
		t.Fatal(err)
	}
	return f, r
}

func TestEveryCommandUsesFixtureSocketAndScrubbedEnv(t *testing.T) {
	def := hostileEnv(t)
	f, r := newMocked(t)
	if err := f.NewSession("fx-a", "", "sleep", "60"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Run("send-keys", "-t", "=fx-a:", "-l", "hi"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) == 0 {
		t.Fatal("nothing executed")
	}
	for i, argv := range r.calls {
		if argv[0] != "-S" || argv[1] != f.Socket() {
			t.Fatalf("call %d not pinned to fixture socket: %v", i, argv)
		}
		if strings.Contains(strings.Join(argv, " "), def) {
			t.Fatalf("call %d references the default socket: %v", i, argv)
		}
		for _, kv := range r.envs[i] {
			for _, k := range []string{"TMUX=", "TMUX_PANE=", "TMUX_TMPDIR="} {
				if strings.HasPrefix(kv, k) {
					t.Fatalf("call %d leaked %s into tmux env", i, kv)
				}
			}
		}
		if argv[2] == "kill-server" {
			t.Fatal("kill-server executed")
		}
	}
	if !strings.HasPrefix(f.Socket(), f.Dir()+string(filepath.Separator)) {
		t.Fatalf("socket %s not inside fixture dir %s", f.Socket(), f.Dir())
	}
}

func TestRejectionsHappenBeforeExecution(t *testing.T) {
	def := hostileEnv(t)
	f, r := newMocked(t)
	cases := map[string][]string{
		"kill-server":               {"kill-server"},
		"socket override -L":        {"-L", "default", "list-sessions"},
		"socket override -S":        {"-S", def, "list-sessions"},
		"config override -f":        {"-f", "/dev/null", "list-sessions"},
		"chained kill-server":       {"list-sessions", ";", "kill-server"},
		"trailing separator":        {"display-message", "-p", "x;"},
		"unlisted command":          {"source-file", "/tmp/x"},
		"new-session via Run":       {"new-session", "-d", "-s", "x"},
		"kill-session via Run":      {"kill-session", "-t", "=x"},
		"kill-session unowned":      {"kill-session", "-t", "=someone-elses"},
		"kill-session prefix match": {"kill-session", "-t", "fx"},
		"empty":                     {},
	}
	for name, args := range cases {
		_, err := f.Run(args...)
		if !errors.Is(err, ErrRejected) {
			t.Errorf("%s: want rejection, got %v", name, err)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("rejected commands reached the executor: %v", r.calls)
	}
	if err := f.KillSession("not-mine"); !errors.Is(err, ErrRejected) {
		t.Fatalf("unowned KillSession: %v", err)
	}
	if err := f.NewSession("bad name;kill-server", ""); !errors.Is(err, ErrRejected) {
		t.Fatalf("bad session name: %v", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("executor was called: %v", r.calls)
	}
}

func TestValidateArgvRejectsMissingDefaultAndUnownedSockets(t *testing.T) {
	def := hostileEnv(t)
	f, _ := newMocked(t)
	other := t.TempDir()
	cases := map[string][]string{
		"missing -S":                    {"list-sessions"},
		"empty socket":                  {"-S", "", "list-sessions"},
		"relative socket":               {"-S", "tmux.sock", "list-sessions"},
		"default socket":                {"-S", def, "list-sessions"},
		"inherited TMUX_TMPDIR default": {"-S", fmt.Sprintf("/tmp/devx-mcp-e2e/tmux/tmux-%d/default", os.Getuid()), "list-sessions"},
		"unowned dir":                   {"-S", filepath.Join(other, "tmux.sock"), "list-sessions"},
		"socket then -L":                {"-S", f.Socket(), "-L", "default", "list-sessions"},
	}
	for name, argv := range cases {
		if err := ValidateArgv(argv, f.Socket(), f.Dir()); !errors.Is(err, ErrRejected) {
			t.Errorf("%s: want rejection, got %v", name, err)
		}
	}
	// A fixture whose dir would make its socket "default" is refused.
	if err := checkSocketPath(filepath.Join(f.Dir(), "default"), f.Dir()); !errors.Is(err, ErrRejected) {
		t.Errorf("socket named default accepted")
	}
	// The inherited $TMUX socket itself is protected even if it were inside
	// a fixture-shaped dir.
	t.Setenv("TMUX", filepath.Join(f.Dir(), "tmux.sock")+",1,1")
	if err := ValidateArgv([]string{"-S", f.Socket(), "list-sessions"}, f.Socket(), f.Dir()); !errors.Is(err, ErrRejected) {
		t.Errorf("inherited $TMUX socket accepted")
	}
}

func TestOwnershipIsDurableAndVerified(t *testing.T) {
	hostileEnv(t)
	f, r := newMocked(t)
	if err := f.NewSession("fx-one", ""); err != nil {
		t.Fatal(err)
	}
	o, err := f.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Sessions) != 1 || o.Sessions[0] != "fx-one" || o.Socket != f.Socket() || o.Nonce == "" || o.PID != os.Getpid() {
		t.Fatalf("owner record: %+v", o)
	}
	// Tampered record: every command is refused, including cleanup.
	data, _ := os.ReadFile(filepath.Join(f.Dir(), ownerFile))
	_ = os.WriteFile(filepath.Join(f.Dir(), ownerFile), []byte(strings.Replace(string(data), o.Nonce, "forged", 1)), 0o600)
	n := len(r.calls)
	if _, err := f.Run("list-sessions"); !errors.Is(err, ErrRejected) {
		t.Fatalf("tampered owner accepted: %v", err)
	}
	if _, err := f.Cleanup(); !errors.Is(err, ErrRejected) {
		t.Fatalf("cleanup with tampered owner: %v", err)
	}
	if len(r.calls) != n {
		t.Fatal("commands executed with a tampered ownership record")
	}
}

func TestCleanupKillsOnlyOwnedSessionsNeverServer(t *testing.T) {
	hostileEnv(t)
	f, r := newMocked(t)
	for _, s := range []string{"fx-a", "fx-b"} {
		if err := f.NewSession(s, ""); err != nil {
			t.Fatal(err)
		}
	}
	// A session on the same server not created via NewSession (e.g. by the
	// code under test) is not in the cleanup scope.
	r.running["not-recorded"] = true
	r.calls = nil
	names, err := f.Cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "fx-a,fx-b" {
		t.Fatalf("cleanup scope %v", names)
	}
	var kills []string
	for _, c := range r.calls {
		switch c[2] {
		case "kill-session":
			kills = append(kills, c[4])
		case "has-session":
		default:
			t.Fatalf("unexpected cleanup command %v", c)
		}
	}
	if strings.Join(kills, ",") != "=fx-a,=fx-b" {
		t.Fatalf("killed %v", kills)
	}
}

// CodeRabbit r4238379176: every fixture tmux command runs with -f /dev/null,
// so the fixture server never loads the developer's tmux.conf or plugins.
// Callers still can never pass -f themselves.
func TestFixtureNeverLoadsUserTmuxConfig(t *testing.T) {
	r := &recorder{running: map[string]bool{}}
	f, err := New(Options{Exec: r.exec})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.NewSession("cfg", t.TempDir(), "sleep 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) == 0 {
		t.Fatal("nothing executed")
	}
	if r.noConfig != len(r.calls) {
		t.Fatalf("%d of %d calls disabled config loading", r.noConfig, len(r.calls))
	}
	if _, err := f.Run("-f", "/tmp/evil.conf", "list-sessions"); !errors.Is(err, ErrRejected) {
		t.Fatalf("caller-supplied -f must be rejected, got %v", err)
	}
}

func TestEnvScrubsTmuxVariables(t *testing.T) {
	hostileEnv(t)
	for _, kv := range Env() {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") || strings.HasPrefix(kv, "TMUX_TMPDIR=") {
			t.Fatalf("Env leaked %s", kv)
		}
	}
}
