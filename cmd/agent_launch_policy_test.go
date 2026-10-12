package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

// ownerLaunchConfig must read only the given owner file: values that viper
// would resolve from a project .devx/config.yaml or DEVX_* environment
// variables never reach the launch policy. Fast guard for the subprocess
// e2e in agent_launch_policy_e2e_test.go.
func TestOwnerLaunchConfigIgnoresViper(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Set("pi_mcp.allowed_projects", []string{"viper-proj"})
	viper.Set("pi_mcp.pi_command", "/repo/evil")
	viper.Set("pi_mcp.pi_args", []string{"--repo-arg"})
	viper.Set("pi_mcp.pass_env", []string{"SYNTH_REPO_VAR"})
	t.Setenv("DEVX_PI_MCP.ALLOWED_PROJECTS", "env-proj")

	dir := t.TempDir()
	owner := filepath.Join(dir, "config.yaml")
	_ = os.WriteFile(owner, []byte("pi_mcp:\n  allowed_projects: [synth]\n  pi_command: /owner/pi\n  pi_args: [--owner-arg]\n  pass_env: [SYNTH_OWNER_VAR]\n"), 0o600)
	got := ownerLaunchConfig(owner)
	if got.LaunchPolicyErr != nil || got.PiCommand != "/owner/pi" ||
		!reflect.DeepEqual(got.AllowedProjects, []string{"synth"}) ||
		!reflect.DeepEqual(got.PiArgs, []string{"--owner-arg"}) ||
		!reflect.DeepEqual(got.PassEnv, []string{"SYNTH_OWNER_VAR"}) {
		t.Fatalf("owner config: %+v", got)
	}
	// Missing owner file: empty policy, not viper's values.
	got = ownerLaunchConfig(filepath.Join(dir, "missing.yaml"))
	if got.LaunchPolicyErr != nil || got.PiCommand != "" || len(got.AllowedProjects)+len(got.PiArgs)+len(got.PassEnv) != 0 {
		t.Fatalf("missing owner file: %+v", got)
	}
	// Invalid owner file: fail closed, nothing from viper.
	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(bad, []byte("pi_mcp:\n  allowed_projects: synth\n"), 0o600)
	got = ownerLaunchConfig(bad)
	if got.LaunchPolicyErr == nil || got.PiCommand != "" || len(got.AllowedProjects) != 0 {
		t.Fatalf("invalid owner file: %+v", got)
	}
}
