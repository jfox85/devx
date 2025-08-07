package caddy

import (
	"encoding/json"
	"testing"
)

func TestRouteGeneration(t *testing.T) {
	route := Route{
		ID: "sess-feat-ui-ui",
		Match: []RouteMatch{
			{
				Host: []string{"feat-ui-ui.localhost"},
			},
		},
		Handle: []RouteHandler{
			{
				Handler: "reverse_proxy",
				Upstreams: []RouteUpstream{
					{
						Dial: "127.0.0.1:3000",
					},
				},
			},
		},
		Terminal: true,
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(route)
	if err != nil {
		t.Fatalf("failed to marshal route: %v", err)
	}

	// Verify key fields are present
	jsonStr := string(jsonData)
	expectedFields := []string{
		`"@id":"sess-feat-ui-ui"`,
		`"host":["feat-ui-ui.localhost"]`,
		`"handler":"reverse_proxy"`,
		`"dial":"127.0.0.1:3000"`,
		`"terminal":true`,
	}

	for _, field := range expectedFields {
		if !contains(jsonStr, field) {
			t.Errorf("expected field %s not found in JSON: %s", field, jsonStr)
		}
	}
}

func TestGetServiceMapping(t *testing.T) {
	tests := []struct {
		portName string
		expected string
	}{
		{"FE_PORT", "ui"},
		{"WEB_PORT", "ui"},
		{"FRONTEND", "ui"},
		{"API_PORT", "api"},
		{"BACKEND", "api"},
		{"DB_PORT", "db"},
		{"DATABASE", "db"},
		{"REDIS_PORT", "redis"},
		{"AUTH_SERVICE_PORT", "auth-service"},
		{"PAYMENT_PORT", "payment"},
		{"CUSTOM_THING_PORT", "custom-thing"},
	}

	for _, test := range tests {
		result := GetServiceMapping(test.portName)
		if result != test.expected {
			t.Errorf("GetServiceMapping(%s) = %s, expected %s",
				test.portName, result, test.expected)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
