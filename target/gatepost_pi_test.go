package target

import "testing"

func TestParseProviderBootstrapOutput(t *testing.T) {
	var result gatepostProviderBootstrap
	result.Env = map[string]string{}
	parseProviderBootstrapOutput("GATEPOST_SECRET_OPENAI_REGISTERED=1\nGATEPOST_SECRET_CODEX_REGISTERED=1\nCODEX_FAKE_JWT=jwt\nCLIPROXYAPI_API_KEY_PLACEHOLDER=GATEPOST_SECRET:cliproxy-key\n", &result)
	if result.Env["CODEX_FAKE_JWT"] != "jwt" {
		t.Fatalf("missing CODEX_FAKE_JWT env: %#v", result.Env)
	}
	if result.Env["CLIPROXYAPI_API_KEY"] != "GATEPOST_SECRET:cliproxy-key" {
		t.Fatalf("missing cliproxy placeholder env: %#v", result.Env)
	}
	if len(result.Registered) != 2 || result.Registered[0] != "codex-oauth" || result.Registered[1] != "openai-key" {
		t.Fatalf("unexpected registered providers: %#v", result.Registered)
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
