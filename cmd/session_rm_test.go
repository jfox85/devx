package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jfox85/devx/piagent"
	"github.com/jfox85/devx/session"
)

func TestAgentsToRetireMatchesExactInstanceOnly(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	sess := &session.Session{Name: "s", InstanceID: "si_aaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: t0}
	agents := []*piagent.Agent{
		{ID: "pa_000000000001", DevxSession: "s", SessionInstanceID: "si_aaaaaaaaaaaaaaaaaaaaaaaa"}, // bound, marker dropped
		{ID: "pa_000000000002", DevxSession: "s", SessionInstanceID: "si_bbbbbbbbbbbbbbbbbbbbbbbb"}, // earlier instance
		{ID: "pa_000000000003", DevxSession: "s"},                                                   // unbound legacy: only via marker
		{ID: "pa_000000000004", DevxSession: "other", SessionInstanceID: "si_aaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	got := agentsToRetire("s", sess, "pa_000000000009", agents)
	if strings.Join(got, ",") != "pa_000000000009,pa_000000000001" {
		t.Fatalf("got %v", got)
	}
	// Id-less record (old writer): exact created_at witness only.
	sess2 := &session.Session{Name: "s", CreatedAt: t0}
	agents[0].SessionCreatedAt = t0
	agents[1].SessionCreatedAt = t0.Add(time.Nanosecond)
	if got := agentsToRetire("s", sess2, "", agents); strings.Join(got, ",") != "pa_000000000001" {
		t.Fatalf("id-less: got %v", got)
	}
}

func TestRemoveGatepostStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stateDir := filepath.Join(home, ".local", "share", "devx", "gatepost", "demo")
	if err := os.MkdirAll(filepath.Join(stateDir, "agent-home", ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "demo"}
	sess.Target.Gatepost.SessionDir = stateDir
	if err := removeGatepostStateDir(sess); err != nil {
		t.Fatalf("removeGatepostStateDir: %v", err)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("state dir still exists or stat failed unexpectedly: %v", err)
	}
}

func TestRemoveGatepostStateDirRejectsOutsideRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sess := &session.Session{Name: "demo"}
	sess.Target.Gatepost.SessionDir = t.TempDir()
	if err := removeGatepostStateDir(sess); err == nil {
		t.Fatalf("expected outside Gatepost state dir to be rejected")
	}
}

func TestRemoveGatepostStateDirRejectsSymlinkStateDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".local", "share", "devx", "gatepost")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(root, "demo")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "demo"}
	sess.Target.Gatepost.SessionDir = link
	if err := removeGatepostStateDir(sess); err == nil {
		t.Fatalf("expected symlinked Gatepost state dir to be rejected")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside target should remain: %v", err)
	}
}

func TestValidateManualWorktreeRemovalAllowsManagedWorktree(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, ".worktrees", "safe")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "safe", ProjectPath: project, Path: path}
	if err := validateManualWorktreeRemoval(sess); err != nil {
		t.Fatalf("expected managed worktree path to validate: %v", err)
	}
}

func TestValidateManualWorktreeRemovalRejectsUnmanagedPath(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	sess := &session.Session{Name: "unsafe", ProjectPath: project, Path: outside}
	if err := validateManualWorktreeRemoval(sess); err == nil {
		t.Fatal("expected unmanaged path to be rejected")
	}
}

// A bound MCP agent never relaunches into a recreated session, even when a
// restored or hand-edited record carries a local-only marker naming it.
// The check fails before any tmux command runs.
func TestEnsureTmuxRefusesRecreatedInstance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 123456789, time.UTC)
	const id = "pa_000000000001"
	write := func(inst string, created time.Time) {
		t.Helper()
		if err := (&session.SessionStore{}).Mutate(func(s *session.SessionStore) error {
			s.Sessions["mcp"] = &session.Session{Name: "mcp", Path: filepath.Join(home, "wt"), CreatedAt: created, InstanceID: inst,
				LocalOnly: &session.LocalOnlyMeta{Owner: session.LocalOnlyOwnerPiMCP, AgentID: id, CreatedAt: created}}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	a := &piagent.Agent{ID: id, DevxSession: "mcp", SessionInstanceID: "si_aaaaaaaaaaaaaaaaaaaaaaaa", SessionCreatedAt: t0}
	for name, c := range map[string]struct {
		inst    string
		created time.Time
	}{
		"recreated with new id":                     {"si_bbbbbbbbbbbbbbbbbbbbbbbb", t0.Add(-time.Hour)},
		"recreated by old writer (no id, new time)": {"", t0.Add(time.Microsecond)},
	} {
		write(c.inst, c.created)
		err := localOnlySessionCreator{}.EnsureTmux("mcp", a)
		if !piagent.IsPermissionDenied(err) || !strings.Contains(err.Error(), "recreated") {
			t.Fatalf("%s: want instance denial, got %v", name, err)
		}
	}
}

// The `session instances` dry run promises to write nothing, so the
// background update check (which writes its state file) is skipped for it,
// but not for --apply/--rollback or other commands.
func TestReadOnlyInvocationSkipsUpdateCheck(t *testing.T) {
	for args, want := range map[string]bool{
		"session instances":                        true,
		"session instances --json":                 true,
		"--config x session instances --confirm a": true,
		"session instances --apply abc":            false,
		"session instances --apply=abc":            false,
		"session instances --rollback /d":          false,
		"session list":                             false,
		"agent list":                               false,
	} {
		if got := isReadOnlyInvocation(strings.Fields(args)); got != want {
			t.Errorf("%q: got %v want %v", args, got, want)
		}
	}
}

// CodeRabbit r4238379169: retirement failures after a session is removed
// must be reported, never swallowed.
func TestRetireSessionAgentsReportsFailures(t *testing.T) {
	sess := &session.Session{Name: "s", ManagedAgent: "pa_000000000001"}
	var out strings.Builder
	retireSessionAgents(&out, "s", sess, func() (*piagent.Manager, error) { return nil, errors.New("store locked") })
	if !strings.Contains(out.String(), "Warning: could not open the managed-agent store") || !strings.Contains(out.String(), "store locked") {
		t.Fatalf("open failure must be reported: %q", out.String())
	}
	// ListAgents failure: the agents directory is unreadable.
	root := t.TempDir()
	st := piagent.NewStore(root)
	_ = os.MkdirAll(filepath.Join(root, "agents"), 0o700)
	_ = os.WriteFile(filepath.Join(root, "agents", "pa_000000000002"), []byte("not a dir"), 0o600)
	_ = os.Chmod(filepath.Join(root, "agents"), 0o000)
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "agents"), 0o700) })
	if _, err := st.ListAgents(); err == nil {
		t.Skip("cannot make ListAgents fail on this platform (running as root?)")
	}
	out.Reset()
	m := piagent.NewManager(st, piagent.Tmux{}, nil, piagent.Config{})
	retireSessionAgents(&out, "s", sess, func() (*piagent.Manager, error) { return m, nil })
	if !strings.Contains(out.String(), "Warning: could not list managed agents") {
		t.Fatalf("list failure must be reported: %q", out.String())
	}
}
