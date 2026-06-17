package target

import "testing"

func TestGatepostLogsCommandIgnoresEnvOverride(t *testing.T) {
	t.Setenv("DEVX_GATEPOST_LOGS_CMD", "/tmp/untrusted-logs")
	cfg := GatepostRuntimeConfig{LogsCommand: "/trusted/gatepost-logs --verbose"}
	cmd, args, dir, err := gatepostLogsCommand(cfg, "", "/tmp/audit.jsonl", 12345)
	if err != nil {
		t.Fatalf("gatepostLogsCommand: %v", err)
	}
	if cmd != "/trusted/gatepost-logs" {
		t.Fatalf("cmd = %q, want trusted config command", cmd)
	}
	if dir != "" {
		t.Fatalf("dir = %q, want empty for explicit command", dir)
	}
	want := []string{"--verbose", "--audit", "/tmp/audit.jsonl", "--listen", "127.0.0.1:12345"}
	if len(args) != len(want) {
		t.Fatalf("args len = %d, want %d: %#v", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q (all args %#v)", i, args[i], want[i], args)
		}
	}
}
