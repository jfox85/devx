package cmd

import "testing"

func TestIsStdioServerInvocation(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"mcp", "pi"}, true},
		{[]string{"--config", "/x/y.yaml", "mcp", "pi"}, true},
		{[]string{"--json", "mcp", "pi"}, true},
		{[]string{"mcp"}, false},
		{[]string{"agent", "list"}, false},
		{[]string{"session", "create", "mcp", "pi"}, false},
		{[]string{"--config", "mcp", "pi"}, false},
	} {
		if got := isStdioServerInvocation(c.args); got != c.want {
			t.Errorf("%v: got %v want %v", c.args, got, c.want)
		}
	}
}
