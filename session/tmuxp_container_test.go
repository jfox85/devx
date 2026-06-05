package session

import (
	"os"
	"strings"
	"testing"
)

func TestStripDockerWrap_NoWrap(t *testing.T) {
	cmd := "cd /workspace && pi -c"
	result := stripDockerWrap(cmd)
	if result != cmd {
		t.Errorf("stripDockerWrap(%q) = %q, want original", cmd, result)
	}
}

func TestStripDockerWrap_SingleWrap(t *testing.T) {
	cmd := `docker exec -it mycontainer bash -lc 'cd /workspace && pi -c'`
	result := stripDockerWrap(cmd)
	if result != "cd /workspace && pi -c" {
		t.Errorf("stripDockerWrap = %q, want %q", result, "cd /workspace && pi -c")
	}
}

func TestStripDockerWrap_DoubleWrap(t *testing.T) {
	inner := "cd /workspace && pi -c"
	doubleWrap := `docker exec -it c1 bash -lc 'docker exec -it c1 bash -lc '"'"'cd /workspace && pi -c'"'"''`
	result := stripDockerWrap(doubleWrap)
	if result != inner {
		t.Errorf("stripDockerWrap(double) = %q, want %q", result, inner)
	}
}

func TestStripDockerWrap_GuardWrap(t *testing.T) {
	guardWrapped := `bash -c 'while true; do state=$(docker inspect ...) ; docker exec -it c1 bash -lc 'echo hello'; read -r; done'`
	result := stripDockerWrap(guardWrapped)
	if result != "echo hello" {
		t.Errorf("stripDockerWrap(guard) = %q, want %q", result, "echo hello")
	}
}

func TestUnescapeBashSingleQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello'", "hello"},
		{`hello'"'"'world'`, "hello'world"},
		{"simple", "simple"},
	}
	for _, tt := range tests {
		got := unescapeBashSingleQuote(tt.input)
		if got != tt.want {
			t.Errorf("unescapeBashSingleQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestWriteGuardScript(t *testing.T) {
	dir := t.TempDir()
	path, err := writeGuardScript(dir, "test-container", "w0-p0",
		[]string{"cd /workspace", "export FOO=bar"},
		"pi -c")
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(content)

	if !strings.Contains(s, "test-container") {
		t.Error("script should reference container name")
	}
	if !strings.Contains(s, "docker exec -it") {
		t.Error("script should use docker exec -it")
	}
	if !strings.Contains(s, "pi -c") {
		t.Error("script should contain the pane command")
	}
	if !strings.Contains(s, "cd /workspace") {
		t.Error("script should contain before commands")
	}
	if !strings.Contains(s, "while true") {
		t.Error("script should loop for reconnection")
	}
	if !strings.Contains(s, "not running") {
		t.Error("script should check container state")
	}
	if !strings.HasPrefix(s, "#!/bin/bash") {
		t.Error("script should have bash shebang")
	}
}

func TestHostTmuxDir(t *testing.T) {
	dir := hostTmuxDir("claude/magical-darwin-3GRgz")
	if !strings.Contains(dir, ".devx") || !strings.Contains(dir, "tmux") {
		t.Errorf("unexpected dir: %s", dir)
	}
	base := dir[strings.LastIndex(dir, "/")+1:]
	if strings.Contains(base, "/") {
		t.Error("directory name should not contain /")
	}
}

func TestCollectStrings(t *testing.T) {
	tests := []struct {
		input    interface{}
		expected []string
	}{
		{"single", []string{"single"}},
		{[]interface{}{"a", "b"}, []string{"a", "b"}},
		{nil, nil},
		{42, nil},
	}
	for _, tt := range tests {
		result := collectStrings(tt.input)
		if len(result) != len(tt.expected) {
			t.Errorf("collectStrings(%v) = %v, want %v", tt.input, result, tt.expected)
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("collectStrings(%v)[%d] = %q, want %q", tt.input, i, result[i], tt.expected[i])
			}
		}
	}
}
