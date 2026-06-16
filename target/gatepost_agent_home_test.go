package target

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatepostAdaptersDirRequiresHookFiles(t *testing.T) {
	root := t.TempDir()
	if got := gatepostAdaptersDir(root); got != "" {
		t.Fatalf("gatepostAdaptersDir without hook files = %q, want empty", got)
	}
	for _, rel := range []string{
		filepath.Join("adapters", "claude", "gatepost-events.py"),
		filepath.Join("adapters", "codex", "gatepost-events.py"),
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	want, err := filepath.EvalSymlinks(filepath.Join(root, "adapters"))
	if err != nil {
		t.Fatal(err)
	}
	if got := gatepostAdaptersDir(root); got != want {
		t.Fatalf("gatepostAdaptersDir = %q, want %q", got, want)
	}
}

func TestWriteGatepostAgentHookConfigs(t *testing.T) {
	r := gatepostRuntime{agentHomeDir: t.TempDir()}
	adapters := t.TempDir()
	if err := writeGatepostAgentHookConfigs(r, adapters); err != nil {
		t.Fatalf("writeGatepostAgentHookConfigs: %v", err)
	}
	for _, rel := range []string{
		filepath.Join(".claude", "settings.json"),
		filepath.Join(".codex", "hooks.json"),
	} {
		path := filepath.Join(r.agentHomeDir, rel)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.Contains(string(body), "gatepost-events") {
			t.Fatalf("%s does not contain gatepost-events: %s", path, body)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", path, got)
		}
	}
}

func TestWriteHookConfigRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	link := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeHookConfig(link, "python3 /opt/gatepost/adapters/claude/gatepost-events.py", nil); err == nil {
		t.Fatalf("writeHookConfig followed symlink; want refusal")
	}
}

func TestPrepareGatepostStateDirsCreatesAgentHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	r, err := newGatepostRuntime("demo/session")
	if err != nil {
		t.Fatalf("newGatepostRuntime: %v", err)
	}
	policyPath := filepath.Join(r.configDir, "policy.gatepost.yaml")
	if err := prepareGatepostStateDirs(r, policyPath); err != nil {
		t.Fatalf("prepareGatepostStateDirs: %v", err)
	}

	wantBase := filepath.Join(home, ".local", "share", "devx", "gatepost", "demo-session")
	if r.sessionDir != wantBase {
		t.Fatalf("sessionDir = %q, want %q", r.sessionDir, wantBase)
	}
	for _, dir := range []string{
		r.auditDir,
		r.configDir,
		r.agentHomeDir,
		filepath.Join(r.agentHomeDir, ".pi", "agent", "sessions"),
		filepath.Join(r.agentHomeDir, ".codex"),
		filepath.Join(r.agentHomeDir, ".claude"),
	} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("expected dir %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}

	for _, file := range []string{
		filepath.Join(r.auditDir, "audit.jsonl"),
		filepath.Join(r.auditDir, "companion.jsonl"),
		policyPath,
	} {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("expected file %s: %v", file, err)
		}
		if info.IsDir() {
			t.Fatalf("%s is a directory", file)
		}
	}
}
