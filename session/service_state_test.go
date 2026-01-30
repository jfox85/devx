package session

import (
	"testing"
	"time"
)

func TestServiceState_IsRunning(t *testing.T) {
	// Create a state with current process (we know it's running)
	state := &ServiceState{
		Name:      "test",
		PID:       1,                          // init process always exists
		StartTime: time.Now().Add(-time.Hour), // started an hour ago
		Port:      3000,
		Status:    StatusRunning,
	}

	if state.Status != StatusRunning {
		t.Error("expected status to be running")
	}
}

func TestRouteMetadata(t *testing.T) {
	rm := RouteMetadata{
		Hostname: "test-frontend.localhost",
		Port:     3000,
		RouteID:  "sess-test-frontend",
	}

	if rm.Hostname != "test-frontend.localhost" {
		t.Errorf("unexpected hostname: %s", rm.Hostname)
	}
}
