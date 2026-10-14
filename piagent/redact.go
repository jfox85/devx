package piagent

import (
	"regexp"
	"unicode/utf8"
)

// Results come from an agent with shell access, so they can echo credentials
// it read from the environment or files. Everything that leaves through MCP
// (results, excerpts, events) is passed through Redact. This is defense in
// depth, not a guarantee: unknown secret formats are not recognized.
var redactions = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z ]*PRIVATE KEY-----|$)`), "[REDACTED PRIVATE KEY]"},
	{regexp.MustCompile(`\b(sk|rk|pk)-(ant-|proj-|live-|test-)?[A-Za-z0-9_\-]{16,}`), "[REDACTED]"},
	{regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{20,}`), "[REDACTED]"},
	{regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`), "[REDACTED]"},
	{regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`), "[REDACTED]"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`), "[REDACTED]"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), "[REDACTED JWT]"},
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{16,}`), "${1}[REDACTED]"},
	{regexp.MustCompile(`(?i)\b([A-Z0-9_]*(api[_-]?key|secret|token|passw(or)?d|credential)[A-Z0-9_]*)(\s*[=:]\s*)("[^"\s]{6,}"|'[^'\s]{6,}'|[^\s"',;]{6,})`), "${1}${4}[REDACTED]"},
}

// Redact masks recognizable credentials in s.
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// tailExcerpt returns at most max bytes from the end of s, cut on a UTF-8
// boundary, and whether it was truncated.
func tailExcerpt(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	start := len(s) - max
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:], true
}

// headExcerpt returns at most max bytes from the start of s on a UTF-8
// boundary.
func headExcerpt(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}
