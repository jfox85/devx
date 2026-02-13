package caddy

import (
	"net/http"
	"testing"
)

func TestCaddyIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewCaddyClient()

	// Check if Caddy is running
	caddyResp, err := http.Get("http://localhost:2019/config/")
	if err != nil {
		t.Skipf("Caddy not available (connection failed): %v", err)
	}
	defer caddyResp.Body.Close()

	if caddyResp.StatusCode != http.StatusOK {
		t.Skipf("Caddy not available (status %d)", caddyResp.StatusCode)
	}

	// Create test sessions
	sessions := map[string]*SessionInfo{
		"integration-test": {
			Name:  "integration-test",
			Ports: map[string]int{"ui": 18080, "api": 18081},
		},
	}

	// Sync routes
	err = SyncRoutes(sessions)
	if err != nil {
		t.Fatalf("SyncRoutes failed: %v", err)
	}

	// Verify routes exist
	routes, err := client.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes failed: %v", err)
	}

	foundUI := false
	foundAPI := false
	for _, route := range routes {
		if route.ID == "sess-integration-test-ui" {
			foundUI = true
		}
		if route.ID == "sess-integration-test-api" {
			foundAPI = true
		}
	}

	if !foundUI {
		t.Error("expected ui route to exist")
	}
	if !foundAPI {
		t.Error("expected api route to exist")
	}

	// Clean up by syncing empty sessions
	err = SyncRoutes(map[string]*SessionInfo{})
	if err != nil {
		t.Fatalf("cleanup SyncRoutes failed: %v", err)
	}

	// Verify routes are gone
	routes, err = client.GetAllRoutes()
	if err != nil {
		t.Fatalf("GetAllRoutes after cleanup failed: %v", err)
	}

	for _, route := range routes {
		if route.ID == "sess-integration-test-ui" || route.ID == "sess-integration-test-api" {
			t.Errorf("route %s should have been removed", route.ID)
		}
	}
}
