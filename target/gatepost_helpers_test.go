package target

import "testing"

func TestGetenvDefault(t *testing.T) {
	t.Run("returns value when set", func(t *testing.T) {
		t.Setenv("DEVX_GETENV_TEST", "actual")
		if got := getenvDefault("DEVX_GETENV_TEST", "fallback"); got != "actual" {
			t.Errorf("getenvDefault = %q, want %q", got, "actual")
		}
	})

	t.Run("trims surrounding whitespace", func(t *testing.T) {
		t.Setenv("DEVX_GETENV_TEST", "  spaced  ")
		if got := getenvDefault("DEVX_GETENV_TEST", "fallback"); got != "spaced" {
			t.Errorf("getenvDefault = %q, want %q", got, "spaced")
		}
	})

	t.Run("returns default when empty", func(t *testing.T) {
		t.Setenv("DEVX_GETENV_TEST", "")
		if got := getenvDefault("DEVX_GETENV_TEST", "fallback"); got != "fallback" {
			t.Errorf("getenvDefault = %q, want %q", got, "fallback")
		}
	})

	t.Run("returns default when only whitespace", func(t *testing.T) {
		t.Setenv("DEVX_GETENV_TEST", "   ")
		if got := getenvDefault("DEVX_GETENV_TEST", "fallback"); got != "fallback" {
			t.Errorf("getenvDefault = %q, want %q", got, "fallback")
		}
	})

	t.Run("returns default when unset", func(t *testing.T) {
		if got := getenvDefault("DEVX_DEFINITELY_UNSET_VAR_XYZ", "fallback"); got != "fallback" {
			t.Errorf("getenvDefault = %q, want %q", got, "fallback")
		}
	})
}

// TestGatepostSecretsFromEnv verifies which provider keys are picked up from
// the environment and that each maps to the correct upstream host, scheme, and
// header — the contract the Gatepost proxy relies on to authenticate requests.
func TestGatepostSecretsFromEnv(t *testing.T) {
	// Clear all provider keys first so we exercise each in isolation.
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("CLIPROXYAPI_API_KEY", "")

	t.Run("no keys yields no secrets", func(t *testing.T) {
		if secrets := gatepostSecretsFromEnv(); len(secrets) != 0 {
			t.Errorf("expected no secrets, got %#v", secrets)
		}
	})

	t.Run("all keys mapped correctly", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-openai")
		t.Setenv("GEMINI_API_KEY", "gem-key")
		t.Setenv("CLIPROXYAPI_API_KEY", "clip-key")

		secrets := gatepostSecretsFromEnv()
		byName := make(map[string]gatepostSecret, len(secrets))
		for _, s := range secrets {
			byName[s.Name] = s
		}

		if len(secrets) != 3 {
			t.Fatalf("expected 3 secrets, got %d: %#v", len(secrets), secrets)
		}

		openai := byName["openai-key"]
		if openai.Host != "api.openai.com" || openai.Scheme != "bearer" || openai.Header != "Authorization" || openai.Value != "sk-openai" {
			t.Errorf("openai secret mapped incorrectly: %#v", openai)
		}

		gemini := byName["gemini-key"]
		if gemini.Host != "generativelanguage.googleapis.com" || gemini.Scheme != "header" || gemini.Header != "x-goog-api-key" || gemini.Value != "gem-key" {
			t.Errorf("gemini secret mapped incorrectly: %#v", gemini)
		}

		cliproxy := byName["cliproxy-key"]
		if cliproxy.Host != "host.docker.internal" || cliproxy.Scheme != "bearer" || cliproxy.Header != "Authorization" || cliproxy.Value != "clip-key" {
			t.Errorf("cliproxy secret mapped incorrectly: %#v", cliproxy)
		}
	})

	t.Run("only set keys are included", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "sk-openai")
		t.Setenv("GEMINI_API_KEY", "")
		t.Setenv("CLIPROXYAPI_API_KEY", "")

		secrets := gatepostSecretsFromEnv()
		if len(secrets) != 1 || secrets[0].Name != "openai-key" {
			t.Fatalf("expected only openai-key, got %#v", secrets)
		}
	})
}
