package web

import (
	"os"
	"testing"

	"github.com/jfox85/devx/internal/tmuxfixture"
)

// TestMain points every tmux call made by this package's tests at a private,
// empty TMUX_TMPDIR (TMUX/TMUX_PANE removed), so code under test can never
// reach a developer's tmux server. See internal/tmuxfixture.
func TestMain(m *testing.M) {
	os.Exit(tmuxfixture.RunGuarded(m))
}
