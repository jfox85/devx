package target

import (
	"os"
	"strings"
	"testing"
)

func TestSanitizeRoleSegment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"croutoncreations", "croutoncreations"},
		{"My-Project 2", "my_project_2"},
		{"__leading", "leading"},
		{"trailing__", "trailing"},
		{"hello world", "hello_world"},
		{"", "unknown"},
		{"---", "unknown"},
	}
	for _, c := range cases {
		if got := sanitizeRoleSegment(c.in); got != c.want {
			t.Errorf("sanitizeRoleSegment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWriteGatepostPolicyIncludesProvidersAndSmart(t *testing.T) {
	path := t.TempDir() + "/policy.gatepost.yaml"
	if err := writeGatepostPolicy(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"unknown_action: smart", "api.openai.com", "api.anthropic.com", "chatgpt.com", "host.docker.internal"} {
		if !strings.Contains(text, want) {
			t.Fatalf("policy missing %q:\n%s", want, text)
		}
	}
}
