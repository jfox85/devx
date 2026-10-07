package tmuxfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Test-only tmux wrapper.
//
// Packages whose production code runs `tmux` by name (cmd, session, web,
// and tmuxp via libtmux) are tested with a wrapper executable placed first
// on PATH. The wrapper is the test binary itself, re-executed in "wrapper
// mode" (selected by DEVX_TMUXWRAP_OWNER, an absolute path to an
// owner.json written by the guarded package). In wrapper mode it:
//
//   - verifies the ownership record (nonce, socket inside the owned dir,
//     not a default or inherited socket);
//   - rejects kill-server/start-server/source-file, caller global options
//     (-L, -S, -f, ...) and ";" chaining;
//   - execs the REAL tmux, resolved to an absolute path before PATH was
//     changed, as `tmux -S <owned socket> <args>` with TMUX, TMUX_PANE and
//     TMUX_TMPDIR removed;
//   - appends every executed argv (or rejection) to the audit log.
//
// So every real tmux exec reached by these tests carries an explicit owned
// -S socket; no server is ever selected from the environment.

const (
	wrapOwnerEnv  = "DEVX_TMUXWRAP_OWNER"
	wrapRealEnv   = "DEVX_TMUXWRAP_REAL"
	wrapNonceEnv  = "DEVX_TMUXWRAP_NONCE"
	wrapRejectRC  = 97
	wrapperBinDir = "bin"
)

// MaybeRunWrapper must be the first call in TestMain. If this process was
// started as the tmux wrapper it never returns.
func MaybeRunWrapper() {
	if os.Getenv(wrapOwnerEnv) == "" || filepath.Base(os.Args[0]) != "tmux" {
		return
	}
	os.Exit(runWrapper(os.Args[1:]))
}

func runWrapper(args []string) int {
	ownerPath := os.Getenv(wrapOwnerEnv)
	dir := filepath.Dir(ownerPath)
	logf := func(format string, a ...any) {
		fh, err := os.OpenFile(filepath.Join(dir, "commands.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(fh, format+"\n", a...)
			_ = fh.Close()
		}
	}
	reject := func(err error) int {
		logf("REJECTED %q: %v", args, err)
		fmt.Fprintf(os.Stderr, "devx test tmux wrapper: %v\n", err)
		return wrapRejectRC
	}
	o, err := readOwner(dir)
	if err != nil || filepath.Join(o.Dir, ownerFile) != ownerPath || o.Nonce != os.Getenv(wrapNonceEnv) {
		return reject(rejectf("ownership record missing or does not match"))
	}
	argv := append([]string{"-S", o.Socket}, args...)
	if err := ValidateArgv(argv, o.Socket, o.Dir); err != nil {
		return reject(err)
	}
	real := os.Getenv(wrapRealEnv)
	if !filepath.IsAbs(real) || filepath.Base(real) != "tmux" || strings.HasPrefix(real, filepath.Join(o.Dir, wrapperBinDir)) {
		return reject(rejectf("real tmux path %q is not an absolute, non-wrapper tmux", real))
	}
	logf("%s", jsonArgv(argv))
	env := scrubWrapperEnv(os.Environ())
	if err := syscall.Exec(real, append([]string{"tmux"}, argv...), env); err != nil {
		fmt.Fprintf(os.Stderr, "devx test tmux wrapper: exec %s: %v\n", real, err)
		return 1
	}
	return 0
}

// scrubWrapperEnv removes routing variables and the wrapper's own
// variables, so nested tmux calls from inside panes do not inherit them.
func scrubWrapperEnv(src []string) []string {
	out := make([]string, 0, len(src))
	for _, kv := range src {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "TMUX", "TMUX_PANE", "TMUX_TMPDIR", wrapOwnerEnv, wrapRealEnv, wrapNonceEnv:
			continue
		}
		out = append(out, kv)
	}
	return out
}

func jsonArgv(a []string) string {
	q := make([]string, len(a))
	for i, s := range a {
		q[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(q, ",") + "]"
}

// Wrapped describes an installed wrapper.
type Wrapped struct {
	Dir      string // fixture-owned directory
	Socket   string // owned socket every exec is pinned to
	BinDir   string // prepended to PATH
	Home     string // fake HOME for the package's tests
	RealTmux string // absolute real tmux ("" if not installed)
}

// InstallWrapper creates the owned dir and socket record, a fake HOME, and
// the wrapper, and rewrites this process's environment:
//
//	PATH        = <dir>/bin:<old PATH>    (bin/tmux -> this test binary)
//	HOME        = <dir>/home              (no real sessions.json/config)
//	XDG_CONFIG_HOME, XDG_STATE_HOME, XDG_CACHE_HOME under the fake HOME
//	TMUX, TMUX_PANE, TMUX_TMPDIR removed
//
// If tmux is not installed the wrapper still installs, so a test that tries
// to exec tmux is rejected instead of finding one elsewhere.
func InstallWrapper() (*Wrapped, func(), error) {
	real, _ := exec.LookPath("tmux")
	if real != "" {
		if abs, err := filepath.Abs(real); err == nil {
			real = abs
		}
	}
	dir, err := os.MkdirTemp("/tmp", "dxtw-")
	if err != nil {
		return nil, nil, err
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	w := &Wrapped{Dir: dir, Socket: filepath.Join(dir, "tmux.sock"), BinDir: filepath.Join(dir, wrapperBinDir), Home: filepath.Join(dir, "home"), RealTmux: real}
	if err := checkSocketPath(w.Socket, dir); err != nil {
		cleanup()
		return nil, nil, err
	}
	for _, d := range []string{w.BinDir, w.Home, filepath.Join(w.Home, ".config")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	self, err := os.Executable()
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if err := os.Symlink(self, filepath.Join(w.BinDir, "tmux")); err != nil {
		cleanup()
		return nil, nil, err
	}
	nonce := randomHex(16)
	if err := writeOwner(dir, &Owner{Nonce: nonce, Socket: w.Socket, Dir: dir, PID: os.Getpid(), Sessions: []string{}}); err != nil {
		cleanup()
		return nil, nil, err
	}
	for _, k := range []string{"TMUX", "TMUX_PANE", "TMUX_TMPDIR"} {
		_ = os.Unsetenv(k)
	}
	set := map[string]string{
		"PATH":            w.BinDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME":            w.Home,
		"XDG_CONFIG_HOME": filepath.Join(w.Home, ".config"),
		"XDG_STATE_HOME":  filepath.Join(w.Home, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(w.Home, ".cache"),
		wrapOwnerEnv:      filepath.Join(dir, ownerFile),
		wrapNonceEnv:      nonce,
		wrapRealEnv:       real,
	}
	for k, v := range set {
		if err := os.Setenv(k, v); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	if real == "" {
		// Point at a path that fails validation: any exec is rejected.
		_ = os.Setenv(wrapRealEnv, "")
	}
	return w, cleanup, nil
}

// WrapperLogPath returns the audit log of a Wrapped install.
func (w *Wrapped) WrapperLogPath() string { return filepath.Join(w.Dir, "commands.log") }
