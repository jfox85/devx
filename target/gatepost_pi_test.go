package target

import "testing"

func TestParseProviderBootstrapOutput(t *testing.T) {
	var result gatepostProviderBootstrap
	result.Env = map[string]string{}
	parseProviderBootstrapOutput("GATEPOST_SECRET_OPENAI_REGISTERED=1\nGATEPOST_SECRET_CODEX_REGISTERED=1\nGATEPOST_SECRET_CLIPROXY_REGISTERED=1\nCODEX_FAKE_JWT=jwt\nCLIPROXYAPI_API_KEY_PLACEHOLDER=GATEPOST_SECRET:cliproxy-key\n", &result)
	if result.Env["CODEX_FAKE_JWT"] != "jwt" {
		t.Fatalf("missing CODEX_FAKE_JWT env: %#v", result.Env)
	}
	if result.Env["CLIPROXYAPI_API_KEY"] != "GATEPOST_SECRET:cliproxy-key" {
		t.Fatalf("missing cliproxy placeholder env: %#v", result.Env)
	}
	want := []string{"anthropic-cli", "cliproxy-key", "codex-oauth", "openai-key"}
	if len(result.Registered) != len(want) {
		t.Fatalf("unexpected registered providers: %#v", result.Registered)
	}
	for i := range want {
		if result.Registered[i] != want[i] {
			t.Fatalf("unexpected registered providers: %#v", result.Registered)
		}
	}
}

func TestRequireGatepostProviders(t *testing.T) {
	cfg := GatepostRuntimeConfig{RequiredProviders: "openai-key, codex-oauth"}
	if err := requireGatepostProviders(cfg, []string{"openai-key", "codex-oauth"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := requireGatepostProviders(cfg, []string{"openai-key"}); err == nil {
		t.Fatal("expected missing provider error")
	}
}

func TestGatepostProviderBootstrapCommandIgnoresEnvOverride(t *testing.T) {
	t.Setenv("DEVX_GATEPOST_PROVIDER_BOOTSTRAP_CMD", "/tmp/untrusted-bootstrap")
	cfg := GatepostRuntimeConfig{ProviderBootstrapCommand: "/trusted/bootstrap --flag"}
	cmd, args, mode, err := gatepostProviderBootstrapCommand(cfg, "", "http://control")
	if err != nil {
		t.Fatalf("gatepostProviderBootstrapCommand: %v", err)
	}
	if cmd != "/trusted/bootstrap" || mode != "command" {
		t.Fatalf("unexpected command/mode: %q %q", cmd, mode)
	}
	if len(args) != 2 || args[0] != "--flag" || args[1] != "http://control" {
		t.Fatalf("unexpected args: %#v", args)
	}
}
