package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPasteTmuxBufferKeepsMultilineTextAsOnePaste drives a real, isolated tmux
// server. The pane runs a program that enables bracketed paste (as Pi, Claude
// Code and modern shells do) and records the raw bytes it receives. A
// multi-line paste must arrive wrapped in bracketed-paste markers; otherwise
// each newline reaches the program as a bare Enter and the text is submitted
// one line at a time.
func TestPasteTmuxBufferKeepsMultilineTextAsOnePaste(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	// Private tmux server: never touch the developer's real sessions.
	tmuxDir, err := os.MkdirTemp("", "devx-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmuxDir) })
	t.Setenv("TMUX_TMPDIR", tmuxDir)
	t.Setenv("TMUX", "")

	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	out := filepath.Join(dir, "out")
	script := "printf '\\033[?2004h'; stty raw -echo; touch '" + ready + "'; exec cat > '" + out + "'"

	const name = "devx-paste-test"
	if err := exec.Command("tmux", "new-session", "-d", "-s", name, "-x", "80", "-y", "24", "sh", "-c", script).Run(); err != nil {
		t.Fatalf("start tmux: %v", err)
	}
	t.Cleanup(func() { exec.Command("tmux", "kill-server").Run() }) //nolint:errcheck

	waitFor(t, func() bool { _, err := os.Stat(ready); return err == nil }, "pane program to start")

	if err := pasteTmuxBuffer("devx-test-buf", "="+name+":", "line one\nline two\nline three", false); err != nil {
		t.Fatalf("pasteTmuxBuffer: %v", err)
	}

	var got string
	waitFor(t, func() bool {
		b, _ := os.ReadFile(out)
		got = string(b)
		return strings.Contains(got, "\x1b[201~")
	}, "bracketed paste end marker")

	// tmux translates LF to CR inside the paste; the receiving program treats
	// CR inside a bracketed paste as a literal newline, not a submit.
	want := "\x1b[200~line one\rline two\rline three\x1b[201~"
	if got != want {
		t.Fatalf("pane received %q, want %q", got, want)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
