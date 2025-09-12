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

func TestSanitizeHostname(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Basic cases
		{"simple", "simple"},
		{"Simple-Case", "simple-case"},
		{"UPPERCASE", "uppercase"},

		// Slash handling (the main issue)
		{"codex/add-ui-for-prompt-presets-in-frontend", "codex-add-ui-for-prompt-presets-in-frontend"},
		{"feature/user-auth", "feature-user-auth"},
		{"fix/api/endpoint", "fix-api-endpoint"},

		// Multiple slashes
		{"deep/nested/branch/name", "deep-nested-branch-name"},

		// Underscore handling
		{"branch_with_underscores", "branch-with-underscores"},
		{"mix_of/slash_and_underscore", "mix-of-slash-and-underscore"},

		// Special characters
		{"branch.with.dots", "branch-with-dots"},
		{"branch@with#special$chars", "branch-with-special-chars"},
		{"branch with spaces", "branch-with-spaces"},

		// Edge cases
		{"", ""},
		{"---multiple---hyphens---", "multiple-hyphens"},
		{"-leading-and-trailing-", "leading-and-trailing"},
		{"123-numeric-456", "123-numeric-456"},

		// Complex real-world examples
		{"feature/auth/oauth2-integration", "feature-auth-oauth2-integration"},
		{"hotfix/payment_processing/stripe_api", "hotfix-payment-processing-stripe-api"},
	}

	for _, test := range tests {
		result := SanitizeHostname(test.input)
		if result != test.expected {
			t.Errorf("SanitizeHostname(%q) = %q, expected %q",
				test.input, result, test.expected)
		}
	}
}

func TestNormalizeDNSName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"UPPERCASE", "uppercase"},
		{"with_underscores", "with-underscores"},
		{"with spaces", "with-spaces"},
		{"with@special#chars", "with-special-chars"},
		{"", ""},
		{"---multiple---hyphens---", "multiple-hyphens"},
		{"-leading-and-trailing-", "leading-and-trailing"},
	}

	for _, test := range tests {
		result := NormalizeDNSName(test.input)
		if result != test.expected {
			t.Errorf("NormalizeDNSName(%q) = %q, expected %q",
				test.input, result, test.expected)
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
