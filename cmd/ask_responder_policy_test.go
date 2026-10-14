package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jfox85/devx/ask"
	"github.com/spf13/viper"
)

// The responder policy must come only from the owner config file: values
// viper resolves from a project .devx/config.yaml or DEVX_* env vars never
// reach it. A broken owner file disables the responder.
func TestOwnerResponderPolicyIgnoresViper(t *testing.T) {
	t.Cleanup(viper.Reset)
	old := cfgFile
	t.Cleanup(func() { cfgFile = old })
	viper.Set("agent_responder.enabled", true)
	viper.Set("agent_responder.mode", "always")
	viper.Set("agent_responder.command", "/repo/evil")
	viper.Set("agent_responder.args", []string{"--repo"})
	t.Setenv("DEVX_AGENT_RESPONDER.ENABLED", "true")

	dir := t.TempDir()
	cfgFile = filepath.Join(dir, "config.yaml")
	_ = os.WriteFile(cfgFile, []byte("agent_responder:\n  enabled: true\n  mode: approval\n  command: /owner/pi\n  args: [--owner]\n"), 0o600)
	p := ask.EffectivePolicy()
	if p.LoadErr != nil || !p.Enabled || p.Mode != "approval" || p.Command != "/owner/pi" || len(p.Args) != 1 || p.Args[0] != "--owner" {
		t.Fatalf("owner policy: %+v", p)
	}
	cfgFile = filepath.Join(dir, "missing.yaml")
	if p := ask.EffectivePolicy(); p.LoadErr != nil || p.Enabled || p.Command == "/repo/evil" {
		t.Fatalf("missing owner file must disable, not fall back to viper: %+v", p)
	}
	cfgFile = filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(cfgFile, []byte("agent_responder:\n  mode: always\n  shell: true\n"), 0o600)
	if p := ask.EffectivePolicy(); p.LoadErr == nil || p.Enabled || p.Command != "" {
		t.Fatalf("invalid owner file must fail closed: %+v", p)
	}
}
