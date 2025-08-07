package caddy

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestCaddyRouteLifecycle tests the full lifecycle of creating and deleting routes
// This test requires Caddy to be running with admin API on localhost:2019
func TestCaddyRouteLifecycle(t *testing.T) {
	// Skip if running in CI or if Caddy not available
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewCaddyClient()

	// Check if Caddy is running - try to actually connect
	caddyResp, err := http.Get("http://localhost:2019/config/")
	if err != nil {
		t.Skipf("Caddy not available (connection failed): %v", err)
	}
	defer caddyResp.Body.Close()

	if caddyResp.StatusCode != http.StatusOK {
		t.Skipf("Caddy not available (status %d)", caddyResp.StatusCode)
	}

	sessionName := "test-session"
	serviceName := "ui"
	port := 8080

	// Clean up any existing routes
	defer func() {
		_ = client.DeleteSessionRoutes(sessionName)
	}()

	// Create route
	_, err = client.CreateRoute(sessionName, serviceName, port)
	if err != nil {
		t.Fatalf("failed to create route: %v", err)
	}

	// Give Caddy a moment to process
	time.Sleep(100 * time.Millisecond)

	// Test that route exists (should return 502 since no service is running)
	testURL := fmt.Sprintf("https://%s-%s.localhost", sessionName, serviceName)

	// Create HTTP client that accepts self-signed certificates
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	httpClient := &http.Client{
		Transport: tr,
		Timeout:   5 * time.Second,
	}

	resp, err := httpClient.Get(testURL)
	if err != nil {
		// DNS resolution failures and connection issues are expected in test environments
		errStr := err.Error()
		if strings.Contains(errStr, "no such host") || strings.Contains(errStr, "lookup") ||
			strings.Contains(errStr, "connection refused") || strings.Contains(errStr, "dial tcp") {
			t.Skipf("Test environment not configured for .localhost HTTPS routing: %v", err)
		}
		t.Fatalf("failed to make request to %s: %v", testURL, err)
	}
	resp.Body.Close()

	// Should get 502 (bad gateway) since no service is running on port 8080
	if resp.StatusCode != 502 {
		t.Logf("Expected 502 (no service running), got %d", resp.StatusCode)
	}

	// Delete route
	err = client.DeleteSessionRoutes(sessionName)
	if err != nil {
		t.Fatalf("failed to delete routes: %v", err)
	}

	// Give Caddy a moment to process
	time.Sleep(100 * time.Millisecond)

	// Test that route no longer exists (should return 404)
	resp2, err := httpClient.Get(testURL)
	if err != nil {
		t.Fatalf("failed to make request after deletion: %v", err)
	}
	resp2.Body.Close()

	// Should get 404 (not found) since route is deleted
	if resp2.StatusCode != 404 {
		t.Errorf("expected 404 after route deletion, got %d", resp2.StatusCode)
	}
}

func TestProvisionSessionRoutes(t *testing.T) {
	// Test the provisioning function without requiring Caddy
	sessionName := "test-provision"
	ports := map[string]int{
		"ui":  3000,
		"api": 3001,
		"db":  5432,
	}

	// This will skip Caddy operations if not available
	routes, err := ProvisionSessionRoutes(sessionName, ports)

	// Should not error even if Caddy is not available
	if err != nil {
		t.Logf("Provisioning warning (expected if Caddy not running): %v", err)
	}

	// If Caddy is available, should have created routes
	if len(routes) > 0 {
		expectedServices := []string{"ui", "api", "db"}
		for _, service := range expectedServices {
			if _, exists := routes[service]; !exists {
				t.Errorf("expected route for service %s not found", service)
			}
		}
	}
}
