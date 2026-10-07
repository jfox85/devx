package tmuxfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// GuardPackage is for TestMain in packages whose production code can run
// tmux without a fixture socket (exact `kill-session -t =<name>`, list,
// has-session, ...). It makes every such call reach a private, empty tmux
// directory instead of the developer's server:
//
//   - TMUX and TMUX_PANE are removed, so an inherited client cannot select
//     the real server.
//   - TMUX_TMPDIR is set to a fresh fixture-owned directory, so the default
//     socket resolves to <dir>/tmux-<uid>/default.
//
// Only kill-session on exact test names and read-only queries happen in
// these packages. Tests that need a running tmux use Fixture. It returns a
// cleanup func that removes the directory; it never kills a server.
func GuardPackage() (cleanup func(), err error) {
	dir, err := os.MkdirTemp("/tmp", "dxtg-")
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"TMUX", "TMUX_PANE"} {
		if err := os.Unsetenv(k); err != nil {
			return nil, err
		}
	}
	if err := os.Setenv("TMUX_TMPDIR", dir); err != nil {
		return nil, err
	}
	return func() { _ = os.RemoveAll(dir) }, nil
}

// RunGuarded is a TestMain helper: GuardPackage, then m.Run.
func RunGuarded(m *testing.M) int {
	cleanup, err := GuardPackage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmuxfixture: cannot isolate tmux: %v\n", err)
		return 1
	}
	defer cleanup()
	return m.Run()
}

// GuardedDir reports the private TMUX_TMPDIR set by GuardPackage, or "".
func GuardedDir() string {
	d := os.Getenv("TMUX_TMPDIR")
	if filepath.Dir(d) == "/tmp" && len(filepath.Base(d)) > 5 && filepath.Base(d)[:5] == "dxtg-" {
		return d
	}
	return ""
}
