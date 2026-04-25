package session

import (
	"unicode"
	"unicode/utf8"
)

// MaxDisplayNameLen is the maximum length of a session display name.
const MaxDisplayNameLen = 100

// Label returns the display name if set, otherwise the session name.
func (s *Session) Label() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Name
}

// EffectiveColor returns the session's color, falling back to AutoColor if unset or invalid.
func (s *Session) EffectiveColor() string {
	if s.Color != "" && IsValidColor(s.Color) {
		return s.Color
	}
	return AutoColor(s.Name)
}

// IsValidDisplayName returns true if dn is a valid display name.
// Empty is valid (used to clear). Max length is 100 characters.
// Control characters (including null bytes, ANSI escapes, newlines) are rejected
// to prevent terminal escape injection.
func IsValidDisplayName(dn string) bool {
	if utf8.RuneCountInString(dn) > MaxDisplayNameLen {
		return false
	}
	for _, r := range dn {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
