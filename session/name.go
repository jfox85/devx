package session

import (
	"regexp"
	"strings"
)

// MaxSessionNameLen is the maximum allowed length for a session name.
// The TUI input and the validation regex both enforce this limit.
const MaxSessionNameLen = 100

// validSessionName matches safe session/branch names: starts with alphanumeric,
// may contain alphanumerics, dots, underscores, hyphens, and forward slashes.
// Forward slashes support branch conventions like "feature/my-thing".
// Use IsValidSessionName for the full check including path-traversal guards.
var validSessionName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/\-]{0,99}$`)

// IsValidSessionName returns true if name passes both the character-class regex
// and path-traversal guards. Names are used as git branch names, tmux targets,
// and path components under .worktrees/, so "." and ".." segments must be rejected.
//
// Validation is two-stage: (1) the regex enforces the allowed character set and
// maximum length; (2) the segment loop enforces structural constraints (path
// traversal, git-rejected patterns) that regex alone cannot cleanly express.
func IsValidSessionName(name string) bool {
	if !validSessionName.MatchString(name) {
		return false
	}
	// Reject trailing slash or consecutive slashes (empty path segments)
	if strings.HasSuffix(name, "/") || strings.Contains(name, "//") {
		return false
	}
	// Reject any segment that is "." or ".." to block path traversal.
	// Also reject segments ending in ".lock" (git refuses such branch names)
	// and segments ending in "." or containing ".." (rejected by git).
	for _, seg := range strings.Split(name, "/") {
		if seg == "." || seg == ".." {
			return false
		}
		if strings.HasSuffix(seg, ".lock") {
			return false
		}
		if strings.HasSuffix(seg, ".") {
			return false
		}
		if strings.Contains(seg, "..") {
			return false
		}
	}
	return true
}
