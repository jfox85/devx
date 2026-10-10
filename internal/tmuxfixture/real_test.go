package tmuxfixture

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealTmuxWithHostileDecoyEnvironment starts a REAL tmux server through
// the fixture while TMUX/TMUX_PANE/TMUX_TMPDIR point at decoys (throwaway
// paths, never the user's default server). It proves the server is created
// at the fixture socket, nothing is created under the decoy TMUX_TMPDIR,
// every executed argv was pinned to the fixture socket, and cleanup removes
// exactly the owned sessions without kill-server.
func TestRealTmuxWithHostileDecoyEnvironment(t *testing.T) {
	if os.Getenv("DEVX_TMUX_FIXTURE_REAL") == "0" {
		t.Skip("DEVX_TMUX_FIXTURE_REAL=0")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	decoyTmp := t.TempDir()
	decoySock := filepath.Join(t.TempDir(), "decoy-server")
	t.Setenv("TMUX_TMPDIR", decoyTmp)
	t.Setenv("TMUX", decoySock+",1,0")
	t.Setenv("TMUX_PANE", "%0")

	f, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Registered before any session exists, so a t.Fatal anywhere below
	// still kills the owned sessions (exact names, owned socket; never
	// kill-server) before the directory is removed. Cleanup is idempotent.
	t.Cleanup(func() {
		_, _ = f.Cleanup()
		_ = os.RemoveAll(f.Dir())
	})
	t.Logf("fixture socket: %s", f.Socket())

	if err := f.NewSession("dxtf-a", "", "sleep", "300"); err != nil {
		t.Fatal(err)
	}
	if err := f.NewSession("dxtf-b", "", "sleep", "300"); err != nil {
		t.Fatal(err)
	}
	out, err := f.Run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Fields(out)[0] != "dxtf-a" || len(strings.Fields(out)) != 2 {
		t.Fatalf("sessions on fixture server: %q", out)
	}
	if st, err := os.Stat(f.Socket()); err != nil || st.Mode()&os.ModeSocket == 0 {
		t.Fatalf("fixture socket not created: %v", err)
	}
	if entries, _ := os.ReadDir(decoyTmp); len(entries) != 0 {
		t.Fatalf("something was created under the decoy TMUX_TMPDIR: %v", entries)
	}
	if _, err := os.Stat(decoySock); !os.IsNotExist(err) {
		t.Fatalf("decoy $TMUX socket was touched: %v", err)
	}

	names, err := f.Cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "dxtf-a,dxtf-b" {
		t.Fatalf("cleanup scope %v", names)
	}
	// With its only sessions gone the private server exits by itself.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := f.Run("has-session", "-t", "=dxtf-a")
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned session survived cleanup")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Audit: every executed argv starts with -S <fixture socket>; no
	// kill-server anywhere.
	fh, err := os.Open(f.CommandLogPath())
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	n := 0
	for sc.Scan() {
		var argv []string
		if err := json.Unmarshal(sc.Bytes(), &argv); err != nil {
			t.Fatal(err)
		}
		n++
		if argv[0] != "-S" || argv[1] != f.Socket() || argv[2] == "kill-server" {
			t.Fatalf("unsafe executed argv: %v", argv)
		}
	}
	if n < 5 {
		t.Fatalf("audit log has %d entries", n)
	}
	if log := os.Getenv("DEVX_TMUX_FIXTURE_PROOF_LOG"); log != "" {
		data, _ := os.ReadFile(f.CommandLogPath())
		_ = os.WriteFile(log, append([]byte("# fixture socket: "+f.Socket()+"\n# decoy TMUX="+decoySock+" TMUX_TMPDIR="+decoyTmp+"\n"), data...), 0o600)
	}
}

// CodeRabbit r4238379176 against a real tmux server: a planted user config
// ($XDG_CONFIG_HOME/tmux/tmux.conf and ~/.tmux.conf) is never loaded.
func TestRealTmuxIgnoresUserConfig(t *testing.T) {
	if os.Getenv("DEVX_TMUX_FIXTURE_REAL") == "0" {
		t.Skip("DEVX_TMUX_FIXTURE_REAL=0")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	_ = os.MkdirAll(filepath.Join(xdg, "tmux"), 0o700)
	_ = os.WriteFile(filepath.Join(xdg, "tmux", "tmux.conf"), []byte("set -g @devx_probe loaded-xdg\n"), 0o600)
	_ = os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("set -g @devx_probe loaded-home\n"), 0o600)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	f, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = f.Cleanup(); _ = os.RemoveAll(f.Dir()) })
	if err := f.NewSession("cfgprobe", t.TempDir(), "sleep 30"); err != nil {
		t.Fatal(err)
	}
	out, err := f.Run("display-message", "-p", "-t", "=cfgprobe", "#{@devx_probe}")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("fixture server loaded a user tmux config: @devx_probe=%q", out)
	}
}
