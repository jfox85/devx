package session

import (
	"strings"
	"testing"
)

func TestIsValidSessionName(t *testing.T) {
	valid := []string{
		"feat-foo",
		"feature/my-thing",
		"a",
		"abc123",
		"my.session",
		"my_session",
		"feature/TICKET-123",
		"a/b/c",
	}
	for _, name := range valid {
		if !IsValidSessionName(name) {
			t.Errorf("expected %q to be valid", name)
		}
	}

	invalid := []string{
		"",
		"my session",       // space
		" leading",         // leading space
		"trailing ",        // trailing space
		"has$dollar",       // dollar sign
		"has(paren)",       // parentheses
		"has`backtick",     // backtick
		"-starts-dash",     // starts with hyphen
		".starts-dot",      // starts with dot
		"/starts-slash",    // starts with slash
		"trailing/",        // trailing slash
		"double//slash",    // consecutive slashes
		"../traversal",     // path traversal
		"a/../b",           // embedded traversal
		"./dot-segment",    // dot segment
		"feat.lock",        // .lock suffix (git lock file)
		"feature/foo.lock", // .lock suffix in segment
		"trailing-dot.",    // trailing dot in segment
		"a/b.",             // trailing dot in last segment
		"foo..bar",         // double-dot within segment
		"a/foo..bar/c",     // double-dot within middle segment
	}
	for _, name := range invalid {
		if IsValidSessionName(name) {
			t.Errorf("expected %q to be invalid", name)
		}
	}
}

// TestIsValidSessionName_LengthBoundary tests the 100-character maximum imposed
// by the regex `{0,99}` suffix (1 required leading char + up to 99 more = 100 total).
func TestIsValidSessionName_LengthBoundary(t *testing.T) {
	// Exactly 100 characters: should be valid.
	name100 := "a" + strings.Repeat("b", 99)
	if !IsValidSessionName(name100) {
		t.Errorf("expected 100-char name to be valid")
	}

	// 101 characters: one over the limit, should be invalid.
	name101 := "a" + strings.Repeat("b", 100)
	if IsValidSessionName(name101) {
		t.Errorf("expected 101-char name to be invalid")
	}

	// Single character: the minimum valid name.
	if !IsValidSessionName("z") {
		t.Errorf("expected single-char name to be valid")
	}
}

// TestIsValidSessionName_AllowedFirstChars checks that every character class
// that is permitted as the first character is actually accepted, and that
// characters that must not start a name are rejected.
func TestIsValidSessionName_AllowedFirstChars(t *testing.T) {
	// Letters (upper and lower) and digits are all valid as a first character.
	valid := []string{
		"Aname", "Zname", "aname", "zname",
		"0name", "9name",
	}
	for _, name := range valid {
		if !IsValidSessionName(name) {
			t.Errorf("expected %q to be valid (letter/digit first char)", name)
		}
	}

	// Symbols that must not appear as the first character.
	invalid := []string{
		"_leading-underscore",
		"-leading-hyphen",
		".leading-dot",
		"/leading-slash",
	}
	for _, name := range invalid {
		if IsValidSessionName(name) {
			t.Errorf("expected %q to be invalid (disallowed first char)", name)
		}
	}
}

// TestIsValidSessionName_PathTraversalVariants exercises path-traversal patterns
// beyond the baseline cases already present in TestIsValidSessionName.
func TestIsValidSessionName_PathTraversalVariants(t *testing.T) {
	invalid := []string{
		"a/.",       // trailing lone dot segment
		"a/..",      // trailing double-dot segment
		"a/./b",     // dot segment in the middle
		"a/b/./c",   // dot segment deeper
		"a/b/../c",  // double-dot segment deeper
		"../a/../b", // multiple traversal segments
	}
	for _, name := range invalid {
		if IsValidSessionName(name) {
			t.Errorf("expected path-traversal name %q to be invalid", name)
		}
	}
}

// TestIsValidSessionName_SlashEdgeCases focuses on the slash-handling rules:
// trailing slash and consecutive slashes are rejected; valid multi-level paths
// are accepted.
func TestIsValidSessionName_SlashEdgeCases(t *testing.T) {
	valid := []string{
		"a/b",
		"a/b/c",
		"feature/sub/ticket",
	}
	for _, name := range valid {
		if !IsValidSessionName(name) {
			t.Errorf("expected slash-separated path %q to be valid", name)
		}
	}

	invalid := []string{
		"a/",    // trailing slash
		"a//b",  // consecutive slashes (empty segment)
		"a///b", // three consecutive slashes
	}
	for _, name := range invalid {
		if IsValidSessionName(name) {
			t.Errorf("expected %q to be invalid (slash rule violation)", name)
		}
	}
}

// TestIsValidSessionName_ForbiddenChars checks that characters outside the
// allowed set [a-zA-Z0-9._/-] are rejected regardless of position.
func TestIsValidSessionName_ForbiddenChars(t *testing.T) {
	forbidden := []string{
		"has@symbol",
		"has!bang",
		"has#hash",
		"has%percent",
		"has^caret",
		"has&ampersand",
		"has*star",
		"has+plus",
		"has=equals",
		"has|pipe",
		"has\\backslash",
		"has:colon",
		"has;semi",
		"has,comma",
		"has<angle",
		"has>angle",
		"has?question",
		"has\"quote",
		"has'single",
		"has[bracket",
		"has]bracket",
		"has{brace",
		"has}brace",
		"has\ttab",
		"has\nnewline",
	}
	for _, name := range forbidden {
		if IsValidSessionName(name) {
			t.Errorf("expected %q to be invalid (forbidden char)", name)
		}
	}
}

// TestIsValidSessionName_Table is a table-driven form of the core cases, providing
// explicit documentation of every rule through descriptive test names.
func TestIsValidSessionName_Table(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// --- valid ---
		{name: "single_letter", input: "a", want: true},
		{name: "single_digit", input: "9", want: true},
		{name: "hyphenated", input: "my-session", want: true},
		{name: "dot_separator", input: "my.session", want: true},
		{name: "underscore", input: "my_session", want: true},
		{name: "branch_style", input: "feature/TICKET-123", want: true},
		{name: "three_levels", input: "a/b/c", want: true},
		{name: "mixed_case", input: "MyFeature", want: true},
		{name: "digits_only_after_letter", input: "a123", want: true},
		{name: "max_length_100", input: "a" + strings.Repeat("x", 99), want: true},

		// --- invalid: length ---
		{name: "empty", input: "", want: false},
		{name: "over_max_length_101", input: "a" + strings.Repeat("x", 100), want: false},

		// --- invalid: first character ---
		{name: "starts_with_dash", input: "-bad", want: false},
		{name: "starts_with_dot", input: ".bad", want: false},
		{name: "starts_with_slash", input: "/bad", want: false},
		{name: "starts_with_underscore", input: "_bad", want: false},

		// --- invalid: path traversal ---
		{name: "double_dot_root", input: "../etc", want: false},
		{name: "double_dot_embedded", input: "a/../b", want: false},
		{name: "single_dot_segment", input: "a/./b", want: false},
		{name: "double_dot_trailing", input: "a/..", want: false},
		{name: "single_dot_trailing", input: "a/.", want: false},

		// --- invalid: slash rules ---
		{name: "trailing_slash", input: "a/", want: false},
		{name: "double_slash", input: "a//b", want: false},

		// --- invalid: forbidden characters ---
		{name: "space_middle", input: "a b", want: false},
		{name: "dollar_sign", input: "a$b", want: false},
		{name: "at_sign", input: "a@b", want: false},
		{name: "backtick", input: "a`b", want: false},

		// --- invalid: git-rejected segment patterns ---
		{name: "lock_suffix", input: "feat.lock", want: false},
		{name: "lock_suffix_in_path", input: "feature/foo.lock", want: false},
		{name: "trailing_dot_in_segment", input: "trailing.", want: false},
		{name: "trailing_dot_in_path_segment", input: "a/b.", want: false},
		{name: "double_dot_within_segment", input: "foo..bar", want: false},
		{name: "double_dot_within_middle_segment", input: "a/foo..bar/c", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidSessionName(tt.input)
			if got != tt.want {
				t.Errorf("IsValidSessionName(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
