package artifactbridge

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseResponderPolicy(t *testing.T) {
	d := DefaultResponderPolicy()
	for _, c := range []struct {
		name, yaml string
		want       *ResponderPolicy
		errHas     string
	}{
		{"absent", "editor: x\n", &d, ""},
		{"empty doc", "", &d, ""},
		{"full", "agent_responder:\n  enabled: true\n  mode: always\n  command: /o/pi\n  args: [-p, '@{{.PromptFile}}']\n  timeout: 2m\n  read_only: false\n",
			&ResponderPolicy{Enabled: true, Mode: "always", Command: "/o/pi", Args: []string{"-p", "@{{.PromptFile}}"}, Timeout: 2 * time.Minute}, ""},
		{"partial keeps defaults", "agent_responder:\n  enabled: true\n",
			&ResponderPolicy{Enabled: true, Mode: d.Mode, Command: d.Command, Args: d.Args, Timeout: d.Timeout, ReadOnly: true}, ""},
		{"bad mode", "agent_responder:\n  mode: sometimes\n", nil, "none, approval or always"},
		{"string bool", "agent_responder:\n  enabled: 'true'\n", nil, "true or false"},
		{"null command", "agent_responder:\n  command: ~\n", nil, "non-empty string"},
		{"args scalar", "agent_responder:\n  args: --x\n", nil, "must be a list"},
		{"bad timeout", "agent_responder:\n  timeout: soon\n", nil, "positive duration"},
		{"unknown key", "agent_responder:\n  shell: true\n", nil, "unknown key"},
		{"duplicate", "agent_responder:\n  enabled: false\n  enabled: true\n", nil, "set twice"},
		{"alias", "x: &a true\nagent_responder:\n  enabled: *a\n", nil, "alias"},
		{"merge key", "base: &b {enabled: true}\nagent_responder:\n  <<: *b\n", nil, "merge keys"},
		{"not mapping", "agent_responder: yes\n", nil, "must be a mapping"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseResponderPolicy([]byte(c.yaml))
			if c.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), c.errHas) {
					t.Fatalf("want error containing %q, got %v (%+v)", c.errHas, err, got)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, *c.want) {
				t.Fatalf("got %+v, %v; want %+v", got, err, *c.want)
			}
		})
	}
}

func TestLoadResponderPolicyFile(t *testing.T) {
	dir := t.TempDir()
	got, err := LoadResponderPolicyFile(filepath.Join(dir, "missing.yaml"))
	if err != nil || got.Enabled {
		t.Fatalf("missing owner file must give the disabled default: %+v %v", got, err)
	}
	p := filepath.Join(dir, "config.yaml")
	_ = os.WriteFile(p, []byte("agent_responder:\n  enabled: true\n"), 0o600)
	if got, err := LoadResponderPolicyFile(p); err != nil || !got.Enabled {
		t.Fatalf("owner file: %+v %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(p, 0o666); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadResponderPolicyFile(p); err == nil {
			t.Fatal("a group/world-writable owner config must be refused")
		}
	}
}
