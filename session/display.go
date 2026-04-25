package session

// MaxDisplayNameLen is the maximum length of a session display name.
const MaxDisplayNameLen = 100

// Label returns the display name if set, otherwise the session name.
func (s *Session) Label() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Name
}

// IsValidDisplayName returns true if dn is a valid display name.
// Empty is valid (used to clear). Max length is 100 characters.
func IsValidDisplayName(dn string) bool {
	return len(dn) <= MaxDisplayNameLen
}
