package session

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfox85/devx/internal/tmuxfixture"
)

// localOnlyRepo makes a git repo inside the guard's fake HOME, shaped like a
// real DevX project: it has a .devx/config.yaml with service ports and a
// session.yaml.tmpl whose windows would start an editor Pi and a service.
// A local-only create must ignore all of it.
func localOnlyRepo(t *testing.T) string {
	t.Helper()
	w := tmuxfixture.ActiveWrapper()
	if w == nil || os.Getenv("HOME") != w.Home {
		t.Skip("owned tmux wrapper / fake HOME not active")
	}
	setupTempHome(t)
	repo := filepath.Join(t.TempDir(), "repo")
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(repo, ".devx"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(filepath.Dir(repo), "init", "-q", "-b", "main", repo)
	_ = os.WriteFile(filepath.Join(repo, ".devx", "config.yaml"), []byte("ports:\n  - WEB\n  - API\nbootstrap_files:\n  - secret.txt\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, ".devx", "session.yaml.tmpl"), []byte("session_name: x\nwindows:\n  - window_name: editor\n    panes:\n      - 'pi -c'\n  - window_name: services\n    panes:\n      - ./start_service.sh web\n"), 0o644)
	_ = os.WriteFile(filepath.Join(repo, "secret.txt"), []byte("s"), 0o600)
	run(repo, "add", ".devx")
	run(repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "init")
	return repo
}

func tmuxOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestLocalOnlyCreateHasNoPortsRoutesOrProjectSideEffects(t *testing.T) {
	repo := localOnlyRepo(t)
	sess, err := CreateLocalOnlySession(LocalOnlyRequest{Name: "lo-a", ProjectAlias: "proj", ProjectPath: repo, AgentID: "pa_1"})
	if err != nil {
		t.Fatal(err)
	}
	if !sess.IsLocalOnly() || sess.LocalOnly.AgentID != "pa_1" || sess.LocalOnly.Owner != LocalOnlyOwnerPiMCP {
		t.Fatalf("marker: %+v", sess.LocalOnly)
	}
	if len(sess.Ports) != 0 || len(sess.Routes) != 0 {
		t.Fatalf("ports=%v routes=%v", sess.Ports, sess.Routes)
	}
	for _, f := range []string{".envrc", ".tmuxp.yaml", "secret.txt"} {
		if _, err := os.Stat(filepath.Join(sess.Path, f)); err == nil {
			t.Fatalf("%s must not be generated/copied for a local-only session", f)
		}
	}
	// The worktree is real and on its own branch.
	b, _ := exec.Command("git", "-C", sess.Path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if strings.TrimSpace(string(b)) != "lo-a" {
		t.Fatalf("branch %q", b)
	}
	// Persisted marker survives a reload.
	st, _ := LoadSessions()
	if s, ok := st.GetSession("lo-a"); !ok || !s.IsLocalOnly() {
		t.Fatal("marker not persisted")
	}
	// tmux: one inert window, no editor/services windows from the template.
	// (tmux is Unix-only; the record/worktree assertions above run everywhere.)
	requireTmux(t)
	if err := EnsureLocalOnlyTmuxSession("lo-a", sess); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "=lo-a").Run() })
	wins := tmuxOut(t, "list-windows", "-t", "=lo-a", "-F", "#{window_name}")
	if wins != LocalOnlyWindow {
		t.Fatalf("windows=%q", wins)
	}
	cmd := tmuxOut(t, "list-panes", "-t", "=lo-a", "-F", "#{pane_start_command}")
	if !strings.Contains(cmd, "tail -f /dev/null") {
		t.Fatalf("first pane must be inert, got %q", cmd)
	}
	// Idempotent: a second ensure adopts the same tagged session.
	if err := EnsureLocalOnlyTmuxSession("lo-a", sess); err != nil {
		t.Fatal(err)
	}
	if n := tmuxOut(t, "list-windows", "-t", "=lo-a", "-F", "x"); n != "x" {
		t.Fatalf("second ensure added windows: %q", n)
	}
}

func TestLocalOnlyCreateResumeAndOwnership(t *testing.T) {
	repo := localOnlyRepo(t)
	req := LocalOnlyRequest{Name: "lo-b", ProjectAlias: "proj", ProjectPath: repo, AgentID: "pa_1"}
	if _, err := CreateLocalOnlySession(req); err != nil {
		t.Fatal(err)
	}
	// Resume by the same agent: same record, no duplicate.
	if _, err := CreateLocalOnlySession(req); err != nil {
		t.Fatalf("resume: %v", err)
	}
	st, _ := LoadSessions()
	if len(st.Sessions) != 1 {
		t.Fatalf("sessions=%d", len(st.Sessions))
	}
	// Another agent cannot adopt it.
	other := req
	other.AgentID = "pa_2"
	if _, err := CreateLocalOnlySession(other); !errors.Is(err, ErrLocalOnlyNotOwned) {
		t.Fatalf("foreign adopt: %v", err)
	}
	// An ordinary (unmarked) session with the name is never adopted.
	if err := st.AddSession("plain", "plain", filepath.Join(repo, ".worktrees", "plain"), map[string]int{"WEB": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLocalOnlySession(LocalOnlyRequest{Name: "plain", ProjectAlias: "proj", ProjectPath: repo, AgentID: "pa_1"}); !errors.Is(err, ErrLocalOnlyNotOwned) {
		t.Fatalf("plain adopt: %v", err)
	}
	// A pre-existing branch (someone else's work) is refused.
	_ = exec.Command("git", "-C", repo, "branch", "taken").Run()
	if _, err := CreateLocalOnlySession(LocalOnlyRequest{Name: "taken", ProjectAlias: "proj", ProjectPath: repo, AgentID: "pa_3"}); !errors.Is(err, ErrLocalOnlyNotOwned) {
		t.Fatalf("branch adopt: %v", err)
	}
	st, _ = LoadSessions()
	if _, ok := st.GetSession("taken"); ok {
		t.Fatal("refused create left a record")
	}
}

func TestLocalOnlyCreateFailureLeavesNothing(t *testing.T) {
	repo := localOnlyRepo(t)
	// Make worktree creation fail: .worktrees is a file.
	if err := os.WriteFile(filepath.Join(repo, ".worktrees"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLocalOnlySession(LocalOnlyRequest{Name: "lo-c", ProjectAlias: "proj", ProjectPath: repo, AgentID: "pa_1"}); err == nil {
		t.Fatal("expected failure")
	}
	st, _ := LoadSessions()
	if _, ok := st.GetSession("lo-c"); ok {
		t.Fatal("failed create left its record")
	}
}

func TestLocalOnlyTmuxRefusesForeignOrUnmarked(t *testing.T) {
	repo := localOnlyRepo(t)
	sess, err := CreateLocalOnlySession(LocalOnlyRequest{Name: "lo-d", ProjectAlias: "proj", ProjectPath: repo, AgentID: "pa_1"})
	if err != nil {
		t.Fatal(err)
	}
	// A same-named tmux session that someone else made is never adopted.
	requireTmux(t)
	tmuxOut(t, "new-session", "-d", "-s", "lo-d", "sleep", "600")
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "=lo-d").Run() })
	if err := EnsureLocalOnlyTmuxSession("lo-d", sess); !errors.Is(err, ErrLocalOnlyNotOwned) {
		t.Fatalf("foreign tmux adopted: %v", err)
	}
	// Missing marker fails closed.
	plain := *sess
	plain.LocalOnly = nil
	if err := EnsureLocalOnlyTmuxSession("lo-d", &plain); err == nil {
		t.Fatal("unmarked session accepted")
	}
	// Incomplete marker fails closed.
	bad := *sess
	bad.LocalOnly = &LocalOnlyMeta{Owner: "someone-else", AgentID: "pa_1"}
	if err := EnsureLocalOnlyTmuxSession("lo-d", &bad); err == nil {
		t.Fatal("foreign owner accepted")
	}
}

func TestLocalOnlyRejectsUnsafeNames(t *testing.T) {
	setupTempHome(t)
	for _, n := range []string{"a/b", "../x", "", "-x"} {
		if _, err := CreateLocalOnlySession(LocalOnlyRequest{Name: n, ProjectPath: "/tmp", AgentID: "pa"}); err == nil {
			t.Fatalf("name %q accepted", n)
		}
	}
}

// requireTmux skips the tmux part of a test where tmux cannot run (Windows,
// or tmux not installed); session-record assertions before it still run.
func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux not available on this platform (%v)", err)
	}
}
