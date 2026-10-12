package artifactbridge

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseLaunchPolicy(t *testing.T) {
	lp, err := ParseLaunchPolicy([]byte(`
web_port: 7777
pi_mcp:
  allowed_projects: [a, b]
  pi_command: /owner/bin/pi
  pi_args: [--model, synth/model]
  pass_env: [SYNTH_VAR]
  artifacts:
    read: true
`))
	want := LaunchPolicy{AllowedProjects: []string{"a", "b"}, PiCommand: "/owner/bin/pi", PiArgs: []string{"--model", "synth/model"}, PassEnv: []string{"SYNTH_VAR"}}
	if err != nil || !reflect.DeepEqual(lp, want) {
		t.Fatalf("got %+v %v", lp, err)
	}
	for _, empty := range []string{"", "web_port: 7777\n", "pi_mcp: {}\n"} {
		lp, err := ParseLaunchPolicy([]byte(empty))
		if err != nil || !reflect.DeepEqual(lp, LaunchPolicy{}) {
			t.Fatalf("%q: %+v %v", empty, lp, err)
		}
	}
	// The launch parser ignores pi_mcp.artifacts content (the bridge
	// validates it separately); a bad artifacts block does not stop starts.
	if _, err := ParseLaunchPolicy([]byte("pi_mcp:\n  allowed_projects: [a]\n  artifacts:\n    session: [x]\n")); err != nil {
		t.Fatalf("artifacts block must not affect launch policy: %v", err)
	}
}

func TestParseLaunchPolicyFailsClosed(t *testing.T) {
	for _, doc := range []string{
		"pi_mcp: [\n",
		"- a\n",
		"pi_mcp: x\n",
		"pi_mcp:\n  allowed_projects: a\n",
		"pi_mcp:\n  allowed_projects: [1]\n",
		"pi_mcp:\n  allowed_projects: [\"\"]\n",
		"pi_mcp:\n  allowed_projects: ~\n",
		"pi_mcp:\n  allowed_projects: [a]\n  allowed_projects: [b]\n",
		"pi_mcp:\n  pi_command: [x]\n",
		"pi_mcp:\n  pi_command: \"\"\n",
		"pi_mcp:\n  pi_command: ~\n",
		"pi_mcp:\n  pi_command: 7\n",
		"pi_mcp:\n  pi_args: --x\n",
		"pi_mcp:\n  pi_args: [{a: b}]\n",
		"pi_mcp:\n  pass_env: SYNTH_VAR\n",
		"pi_mcp:\n  pass_env: [true]\n",
		"x: &a [p]\npi_mcp:\n  allowed_projects: *a\n",
		"base: &b {allowed_projects: [p]}\npi_mcp:\n  <<: *b\n",
		"pi_mcp: {}\npi_mcp: {allowed_projects: [p]}\n",
	} {
		if lp, err := ParseLaunchPolicy([]byte(doc)); err == nil {
			t.Fatalf("%q must fail, got %+v", doc, lp)
		}
	}
}

func TestLoadLaunchPolicyFile(t *testing.T) {
	dir := t.TempDir()
	if lp, err := LoadLaunchPolicyFile(filepath.Join(dir, "missing.yaml")); err != nil || !reflect.DeepEqual(lp, LaunchPolicy{}) {
		t.Fatalf("missing file must give the empty policy: %+v %v", lp, err)
	}
	if _, err := LoadLaunchPolicyFile(""); err == nil {
		t.Fatal("no path must fail")
	}
	if _, err := LoadLaunchPolicyFile(dir); err == nil {
		t.Fatal("directory must fail")
	}
	big := filepath.Join(dir, "big.yaml")
	_ = os.WriteFile(big, []byte(strings.Repeat("#", maxPolicyFileBytes+1)), 0o600)
	if _, err := LoadLaunchPolicyFile(big); err == nil {
		t.Fatal("oversized config must fail")
	}
	good := "pi_mcp:\n  allowed_projects: [proj]\n  pi_command: /owner/pi\n"
	if platformSupported {
		for _, mode := range []os.FileMode{0o620, 0o602, 0o666} {
			p := filepath.Join(dir, fmt.Sprintf("w%o.yaml", mode))
			_ = os.WriteFile(p, []byte(good), 0o600)
			_ = os.Chmod(p, mode)
			if _, err := LoadLaunchPolicyFile(p); err == nil {
				t.Fatalf("mode %o must fail", mode)
			}
		}
		fifo := filepath.Join(dir, "fifo.yaml")
		if mkfifo(fifo) == nil {
			if _, err := LoadLaunchPolicyFile(fifo); err == nil {
				t.Fatal("FIFO must fail")
			}
		}
	}
	ok := filepath.Join(dir, "ok.yaml")
	_ = os.WriteFile(ok, []byte(good), 0o644)
	if lp, err := LoadLaunchPolicyFile(ok); err != nil || lp.PiCommand != "/owner/pi" || len(lp.AllowedProjects) != 1 {
		t.Fatalf("0644 owner config must load: %+v %v", lp, err)
	}
}
