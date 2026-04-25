package session

import (
	"fmt"
	"testing"
)

func TestIsValidColor(t *testing.T) {
	valid := []string{"red", "blue", "green", "yellow", "purple", "orange", "pink", "cyan"}
	for _, c := range valid {
		if !IsValidColor(c) {
			t.Errorf("expected %q to be valid", c)
		}
	}

	invalid := []string{"", "RED", "Red", "magenta", "white", "black", "#ff0000", "123"}
	for _, c := range invalid {
		if IsValidColor(c) {
			t.Errorf("expected %q to be invalid", c)
		}
	}
}

func TestAutoColor(t *testing.T) {
	// Deterministic: same name always gets the same color
	c1 := AutoColor("my-session")
	c2 := AutoColor("my-session")
	if c1 != c2 {
		t.Errorf("AutoColor not deterministic: got %q and %q", c1, c2)
	}

	// Result is always a valid color
	names := []string{"a", "test", "feature/my-branch", "x/y/z", ""}
	for _, name := range names {
		c := AutoColor(name)
		if !IsValidColor(c) {
			t.Errorf("AutoColor(%q) returned invalid color %q", name, c)
		}
	}

	// Different names should produce some variety (not all the same)
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		seen[AutoColor(fmt.Sprintf("session-%d", i))] = true
	}
	if len(seen) < 3 {
		t.Errorf("AutoColor lacks variety: only %d distinct colors from 100 inputs", len(seen))
	}
}
