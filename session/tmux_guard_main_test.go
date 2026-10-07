package session

import (
	"os"
	"testing"

	"github.com/jfox85/devx/internal/tmuxfixture"
)

// TestMain installs the test-only tmux wrapper: every real tmux exec made by
// this package's tests (directly, via production code, or via tmuxp) runs as
// "tmux -S <owned socket>" with TMUX/TMUX_PANE/TMUX_TMPDIR removed, and
// HOME points at a fake test store. See internal/tmuxfixture.
func TestMain(m *testing.M) {
	os.Exit(tmuxfixture.RunGuarded(m))
}
