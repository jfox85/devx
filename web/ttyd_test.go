package web

import (
	"testing"
	"time"
)

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
