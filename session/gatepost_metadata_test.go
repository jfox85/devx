package session

import (
	"encoding/json"
	"testing"
)

func TestGatepostMetaOmitsTokensFromJSON(t *testing.T) {
	meta := TargetMeta{Type: "gatepost", Gatepost: GatepostMeta{Enabled: true, Runtime: "docker-mitmproxy", LogsURL: "http://127.0.0.1:1/?token=abc", ControlToken: "secret-control", EventToken: "secret-event"}}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if contains(text, "secret-control") || contains(text, "secret-event") {
		t.Fatalf("tokens leaked in json: %s", text)
	}
	if !contains(text, "docker-mitmproxy") {
		t.Fatalf("expected runtime metadata: %s", text)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
