package session

import (
	"strings"
	"testing"
)

func TestLabel(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		want        string
	}{
		{name: "my-session", displayName: "", want: "my-session"},
		{name: "my-session", displayName: "My Feature", want: "My Feature"},
		{name: "jf-add-web", displayName: "Web UI", want: "Web UI"},
	}
	for _, tt := range tests {
		s := &Session{Name: tt.name, DisplayName: tt.displayName}
		got := s.Label()
		if got != tt.want {
			t.Errorf("Session{Name:%q, DisplayName:%q}.Label() = %q, want %q",
				tt.name, tt.displayName, got, tt.want)
		}
	}
}

func TestIsValidDisplayName(t *testing.T) {
	// Valid display names
	valid := []string{"My Feature", "a", "Web UI Feature", strings.Repeat("x", 100)}
	for _, dn := range valid {
		if !IsValidDisplayName(dn) {
			t.Errorf("expected %q to be valid display name", dn)
		}
	}

	// Invalid: too long
	if IsValidDisplayName(strings.Repeat("x", 101)) {
		t.Error("expected 101-char display name to be invalid")
	}

	// Empty is valid (means "clear")
	if !IsValidDisplayName("") {
		t.Error("expected empty display name to be valid")
	}
}
