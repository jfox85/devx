package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTtydArgsIncludeMobileScrollback(t *testing.T) {
	args := ttydArgs("demo/session", 7681)
	want := fmt.Sprintf("scrollback=%d", ttydScrollbackLines)
	found := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-t" && args[i+1] == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected ttyd args to include %q, got %v", want, args)
	}
}

func TestTtydArgsUseExactMatchTarget(t *testing.T) {
	args := ttydArgs("demo/session", 7681)
	joined := fmt.Sprint(args)
	if !containsSequence(args, []string{"tmux", "new-session", "-A", "-s", "demo/session-web", "-t", "=demo/session"}) {
		t.Fatalf("expected exact-match tmux target in args, got %s", joined)
	}
}

func TestApplyMobileTmuxOptionsAttemptsBaseAndWebTargets(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "tmux.log")
	scriptPath := filepath.Join(tmpDir, "tmux")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "has-session" ] && [ "$2" = "-t" ] && [ "$3" = "=demo-web" ]; then
  exit 0
fi
echo "$@" >> %s
exit 0
`, logPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write tmux stub: %v", err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+oldPath)

	applyMobileTmuxOptions("demo")

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read tmux log: %v", err)
	}
	log := string(data)
	for _, want := range []string{
		"set-option -t =demo mouse off",
		"set-option -t =demo history-limit 50000",
		"set-option -t =demo-web mouse off",
		"set-option -t =demo-web history-limit 50000",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("expected tmux log to contain %q, got %s", want, log)
		}
	}
}

func TestTtydManagerStartStop(t *testing.T) {
	m := newTtydManager()

	// Use a stub command that stays alive briefly instead of real ttyd
	port, err := m.startForSession("test-session", "sleep", "0.1")
	if err != nil {
		t.Fatalf("startForSession returned error: %v", err)
	}
	if port <= 0 {
		t.Errorf("expected valid port, got %d", port)
	}

	// Should return same port on second call (already running)
	port2, err := m.startForSession("test-session", "sleep", "0.1")
	if err != nil {
		t.Fatalf("second startForSession returned error: %v", err)
	}
	if port2 != port {
		t.Errorf("expected same port %d on second call, got %d", port, port2)
	}

	// Wait for process to exit naturally
	time.Sleep(200 * time.Millisecond)

	m.stopSession("test-session")
	// Verify entry cleaned up (no panic, no port reuse issue)
}

func containsSequence(haystack, needle []string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
