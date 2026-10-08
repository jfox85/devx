package tmuxfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The wrapper is exercised as a real child process: this test binary,
// symlinked as "tmux" (exactly how InstallWrapper wires it). The "real tmux"
// it would exec is a NON-EXECUTING shim that only records its argv and the
// TMUX* variables it received, so these tests never start a tmux server.

func TestMain(m *testing.M) {
	MaybeRunWrapper()
	os.Exit(m.Run())
}

type wrapperRig struct {
	dir, socket, nonce, wrapper, shim, shimLog string
}

func newWrapperRig(t *testing.T) *wrapperRig {
	t.Helper()
	base := t.TempDir()
	dir, err := os.MkdirTemp("/tmp", "dxtw-t-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	r := &wrapperRig{dir: dir, socket: filepath.Join(dir, "tmux.sock"), nonce: randomHex(8)}
	if err := writeOwner(dir, &Owner{Nonce: r.nonce, Socket: r.socket, Dir: dir, Sessions: []string{}}); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	r.wrapper = filepath.Join(base, "wbin", "tmux")
	_ = os.MkdirAll(filepath.Dir(r.wrapper), 0o700)
	if err := os.Symlink(self, r.wrapper); err != nil {
		t.Fatal(err)
	}
	r.shimLog = filepath.Join(base, "shim.log")
	r.shim = filepath.Join(base, "realbin", "tmux")
	_ = os.MkdirAll(filepath.Dir(r.shim), 0o700)
	script := "#!/bin/sh\nprintf '%s\\n' \"TMUX=[${TMUX:-}] TMUX_TMPDIR=[${TMUX_TMPDIR:-}] TMUX_PANE=[${TMUX_PANE:-}] ARGV=$*\" >> '" + r.shimLog + "'\nexit 0\n"
	if err := os.WriteFile(r.shim, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return r
}

// run invokes the wrapper with a hostile inherited environment pointing at
// the user's REAL default socket path.
func (r *wrapperRig) run(t *testing.T, extraEnv []string, args ...string) (int, string) {
	t.Helper()
	def := fmt.Sprintf("/private/tmp/tmux-%d/default", os.Getuid())
	cmd := exec.Command(r.wrapper, args...)
	cmd.Env = append(scrubWrapperEnv(os.Environ()),
		"TMUX="+def+",6528,62", "TMUX_PANE=%244", "TMUX_TMPDIR=/private/tmp",
		wrapOwnerEnv+"="+filepath.Join(r.dir, ownerFile), wrapNonceEnv+"="+r.nonce, wrapRealEnv+"="+r.shim)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

func (r *wrapperRig) shimLines() []string {
	data, _ := os.ReadFile(r.shimLog)
	s := strings.TrimSpace(string(data))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestWrapperPinsOwnedSocketAndScrubsHostileEnv(t *testing.T) {
	r := newWrapperRig(t)
	for _, args := range [][]string{
		{"-V"},
		{"list-sessions", "-F", "#{session_name}"},
		{"kill-session", "-t", "=feat-foo"},
		{"has-session", "-t", "=x"},
		{"new-session", "-d", "-s", "s1"},
	} {
		if rc, out := r.run(t, nil, args...); rc != 0 {
			t.Fatalf("%v: rc=%d %s", args, rc, out)
		}
	}
	lines := r.shimLines()
	if len(lines) != 5 {
		t.Fatalf("shim saw %d execs: %v", len(lines), lines)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "TMUX=[] TMUX_TMPDIR=[] TMUX_PANE=[] ARGV=-S "+r.socket+" ") {
			t.Fatalf("exec not pinned/scrubbed: %s", l)
		}
	}
}

func TestWrapperRejectsBeforeExec(t *testing.T) {
	r := newWrapperRig(t)
	def := fmt.Sprintf("/private/tmp/tmux-%d/default", os.Getuid())
	cases := map[string][]string{
		"kill-server":         {"kill-server"},
		"start-server":        {"start-server"},
		"source-file":         {"source-file", "/etc/x"},
		"-L override":         {"-L", "default", "list-sessions"},
		"-S default override": {"-S", def, "list-sessions"},
		"-S missing value":    {"-S"},
		"-f override":         {"-f", "/dev/null", "list-sessions"},
		"-V with more":        {"-V", "list-sessions"},
		"chained kill-server": {"list-sessions", ";", "kill-server"},
		"trailing ; chain":    {"has-session", "-t", "x;"},
		"no command":          {},
	}
	for name, args := range cases {
		if rc, _ := r.run(t, nil, args...); rc != wrapRejectRC {
			t.Errorf("%s: rc=%d, want rejection %d", name, rc, wrapRejectRC)
		}
	}
	// Default/missing/unowned targets and forged ownership.
	other := t.TempDir()
	_ = writeOwner(other, &Owner{Nonce: "n", Socket: filepath.Join(other, "tmux.sock"), Dir: other})
	envCases := map[string][]string{
		"wrong nonce":          {wrapNonceEnv + "=forged"},
		"missing owner":        {wrapOwnerEnv + "=" + filepath.Join(t.TempDir(), ownerFile)},
		"unowned owner record": {wrapOwnerEnv + "=" + filepath.Join(other, ownerFile)},
		"relative real tmux":   {wrapRealEnv + "=tmux"},
		"real tmux is wrapper": {wrapRealEnv + "=" + r.wrapper + "x"},
	}
	for name, env := range envCases {
		if rc, _ := r.run(t, env, "list-sessions"); rc != wrapRejectRC {
			t.Errorf("%s: rc=%d, want rejection", name, rc)
		}
	}
	// Owner record whose socket is the default socket / outside dir.
	for name, sock := range map[string]string{"default socket": def, "socket outside dir": filepath.Join(other, "tmux.sock"), "socket named default": filepath.Join(r.dir, "default")} {
		_ = writeOwner(r.dir, &Owner{Nonce: r.nonce, Socket: sock, Dir: r.dir})
		if rc, _ := r.run(t, nil, "list-sessions"); rc != wrapRejectRC {
			t.Errorf("owner %s: rc=%d, want rejection", name, rc)
		}
	}
	if n := len(r.shimLines()); n != 0 {
		t.Fatalf("rejected invocations reached the real tmux %d times: %v", n, r.shimLines())
	}
	audit, _ := os.ReadFile(filepath.Join(r.dir, "commands.log"))
	if strings.Count(string(audit), "REJECTED") < len(cases)+3 {
		t.Fatalf("rejections not audited:\n%s", audit)
	}
}

func TestInstallWrapperEnvironment(t *testing.T) {
	def := fmt.Sprintf("/private/tmp/tmux-%d/default", os.Getuid())
	t.Setenv("TMUX", def+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("TMUX_TMPDIR", "/private/tmp")
	t.Setenv("PATH", os.Getenv("PATH"))
	t.Setenv("HOME", os.Getenv("HOME"))
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", wrapOwnerEnv, wrapNonceEnv, wrapRealEnv} {
		t.Setenv(k, os.Getenv(k))
	}
	w, cleanup, err := InstallWrapper()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, k := range []string{"TMUX", "TMUX_PANE", "TMUX_TMPDIR"} {
		if _, ok := os.LookupEnv(k); ok {
			t.Errorf("%s still set", k)
		}
	}
	if os.Getenv("HOME") != w.Home || !strings.HasPrefix(w.Home, w.Dir) {
		t.Errorf("HOME=%s not the fake home", os.Getenv("HOME"))
	}
	if p, _ := exec.LookPath("tmux"); p != filepath.Join(w.BinDir, "tmux") {
		t.Errorf("tmux on PATH resolves to %s, not the wrapper", p)
	}
	if w.RealTmux != "" && strings.HasPrefix(w.RealTmux, w.Dir) {
		t.Errorf("real tmux points into the wrapper dir")
	}
	if !strings.HasPrefix(w.Socket, w.Dir+"/") {
		t.Errorf("socket %s outside owned dir", w.Socket)
	}
}

func TestWrapperWithoutOwnerEnvFailsClosed(t *testing.T) {
	r := newWrapperRig(t)
	cmd := exec.Command(r.wrapper, "list-sessions")
	cmd.Env = append(scrubWrapperEnv(os.Environ()), "TMUX=/private/tmp/tmux-501/default,1,0", wrapRealEnv+"="+r.shim)
	out, err := cmd.CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != wrapRejectRC {
		t.Fatalf("want rejection, got %v: %s", err, out)
	}
	if strings.Contains(string(out), "PASS") || strings.Contains(string(out), "=== RUN") {
		t.Fatal("wrapper fell through to running tests")
	}
	if len(r.shimLines()) != 0 {
		t.Fatal("real tmux reached")
	}
}
