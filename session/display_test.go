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
	valid := []string{"My Feature", "a", "Web UI Feature", strings.Repeat("x", 100), "café", "日本語"}
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

	// Invalid: control characters
	controlChars := []string{
		"\x00null",           // null byte
		"\x1b[31mred\x1b[0m", // ANSI escape
		"line\nbreak",        // newline
		"line\rreturn",       // carriage return
		"\ttabbed",           // tab
		"\x07bell",           // bell
	}
	for _, dn := range controlChars {
		if IsValidDisplayName(dn) {
			t.Errorf("expected display name with control chars to be invalid: %q", dn)
		}
	}
}

func TestEffectiveColor(t *testing.T) {
	// Explicit valid color is returned as-is
	s := &Session{Name: "test", Color: "blue"}
	if got := s.EffectiveColor(); got != "blue" {
		t.Errorf("expected blue, got %q", got)
	}

	// Empty color falls back to AutoColor
	s2 := &Session{Name: "test", Color: ""}
	if got := s2.EffectiveColor(); !IsValidColor(got) {
		t.Errorf("expected valid auto-color, got %q", got)
	}

	// Invalid stored color falls back to AutoColor
	s3 := &Session{Name: "test", Color: "invalid-color"}
	if got := s3.EffectiveColor(); !IsValidColor(got) {
		t.Errorf("expected valid auto-color for invalid stored color, got %q", got)
	}
}
