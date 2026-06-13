package target

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterGatepostSecretsFromHostEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("CLIPROXYAPI_API_KEY", "clip-test")
	t.Setenv("GEMINI_API_KEY", "")
	var got []gatepostSecret
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "Bearer token" {
			t.Fatalf("missing auth header")
		}
		var secret gatepostSecret
		if err := json.NewDecoder(r.Body).Decode(&secret); err != nil {
			t.Fatal(err)
		}
		got = append(got, secret)
		_, _ = w.Write([]byte(`{"name":"ok"}`))
	}))
	defer server.Close()
	registered, err := registerGatepostSecrets(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	if len(registered) != 2 || registered[0] != "openai-key" || registered[1] != "cliproxy-key" {
		t.Fatalf("unexpected registered providers: %#v", registered)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 secrets, got %#v", got)
	}
	if got[0].Value == "" || got[1].Value == "" {
		t.Fatalf("expected secret values")
	}
}
