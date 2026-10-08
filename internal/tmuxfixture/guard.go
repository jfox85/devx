package tmuxfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// RunGuarded is the TestMain for packages whose code runs tmux by name
// (cmd, session, web):
//
//	func TestMain(m *testing.M) { os.Exit(tmuxfixture.RunGuarded(m)) }
//
// It (1) serves wrapper-mode invocations, (2) installs the test-only tmux
// wrapper so every real tmux exec carries an explicit owned -S socket, and
// sets a fake HOME so metadata side effects stay in a test store, (3) runs
// the tests, (4) removes any sessions left on the OWNED socket by exact
// name (never kill-server; nothing else lives on that socket), and removes
// the directory.
func RunGuarded(m *testing.M) int {
	MaybeRunWrapper()
	pyBase := pythonUserBase() // before HOME changes; tmuxp needs it
	w, cleanup, err := InstallWrapper()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmuxfixture: cannot install tmux wrapper: %v\n", err)
		return 1
	}
	if pyBase != "" && os.Getenv("PYTHONUSERBASE") == "" {
		_ = os.Setenv("PYTHONUSERBASE", pyBase)
	}
	code := runWithActiveWrapper(w, m)
	if err := w.killOwnedSessions(); err != nil {
		fmt.Fprintf(os.Stderr, "tmuxfixture: owned-socket cleanup: %v\n", err)
	}
	if dst := os.Getenv("DEVX_TMUXWRAP_PROOF_DIR"); dst != "" {
		if data, err := os.ReadFile(w.WrapperLogPath()); err == nil {
			name := strings.ReplaceAll(filepath.Base(os.Args[0]), ".test", "")
			_ = os.WriteFile(filepath.Join(dst, name+".wrapper-audit.log"), append([]byte("# owned socket "+w.Socket+"\n# fake HOME "+w.Home+"\n"), data...), 0o600)
		}
	}
	cleanup()
	return code
}

var activeWrapper *Wrapped

// runWithActiveWrapper exposes w to tests for the duration of m.Run and
// clears it afterwards, even if m.Run panics.
func runWithActiveWrapper(w *Wrapped, m *testing.M) int {
	activeWrapper = w
	defer func() { activeWrapper = nil }()
	return m.Run()
}

// ActiveWrapper returns the wrapper installed by RunGuarded, if any.
func ActiveWrapper() *Wrapped { return activeWrapper }

// killOwnedSessions lists sessions on the owned socket and kills each by
// exact name. Both commands go through the validated argv path.
func (w *Wrapped) killOwnedSessions() error {
	if w.RealTmux == "" {
		return nil
	}
	if _, err := os.Stat(w.Socket); err != nil {
		return nil // server never started
	}
	run := func(args ...string) (string, error) {
		argv := append([]string{"-S", w.Socket}, args...)
		if err := ValidateArgv(argv, w.Socket, w.Dir); err != nil {
			return "", err
		}
		cmd := exec.Command(w.RealTmux, argv...)
		cmd.Env = scrubWrapperEnv(os.Environ())
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	out, err := run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil // no server / no sessions
	}
	for _, name := range strings.Fields(out) {
		if _, err := run("kill-session", "-t", "="+name); err != nil {
			return err
		}
	}
	return nil
}

// pythonUserBase returns the user site base for the interpreter that runs
// tmuxp (from its shebang), computed with the real HOME before the fake
// HOME is installed. tmuxp installed with `pip --user` needs it.
func pythonUserBase() string {
	if v := os.Getenv("PYTHONUSERBASE"); v != "" {
		return v
	}
	interp := "python3"
	if p, err := exec.LookPath("tmuxp"); err == nil {
		if data, err := os.ReadFile(p); err == nil {
			line, _, _ := strings.Cut(string(data), "\n")
			if f := strings.Fields(strings.TrimPrefix(line, "#!")); len(f) > 0 && filepath.IsAbs(f[0]) && filepath.Base(f[0]) != "env" {
				interp = f[0]
			}
		}
	}
	out, err := exec.Command(interp, "-c", "import site;print(site.getuserbase())").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
