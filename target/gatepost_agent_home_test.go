package target

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfox85/devx/session"
)

func TestGatepostAdaptersDirRequiresHookFiles(t *testing.T) {
	root := t.TempDir()
	if got, err := gatepostAdaptersDir(root); err == nil || got != "" {
		t.Fatalf("gatepostAdaptersDir without hook files = %q, %v; want empty with error", got, err)
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
	got, err := gatepostAdaptersDir(root)
	if err != nil {
		t.Fatalf("gatepostAdaptersDir unexpected error: %v", err)
	}
	if got != want {
		t.Fatalf("gatepostAdaptersDir = %q, want %q", got, want)
	}
}

func TestGatepostAdaptersDirRejectsWritableTrustedPaths(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		filepath.Join("adapters", "claude", "gatepost-events.py"),
		filepath.Join("adapters", "codex", "gatepost-events.py"),
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "adapters", "claude", "gatepost-events.py"), 0o664); err != nil {
		t.Fatal(err)
	}
	if got, err := gatepostAdaptersDir(root); err == nil || got != "" {
		t.Fatalf("gatepostAdaptersDir with group-writable adapter = %q, %v; want empty with error", got, err)
	}
}

func TestGatepostAdaptersDirRejectsWritableIntermediateDir(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		filepath.Join("adapters", "claude", "gatepost-events.py"),
		filepath.Join("adapters", "codex", "gatepost-events.py"),
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(root, "adapters", "claude"), 0o775); err != nil {
		t.Fatal(err)
	}
	if got, err := gatepostAdaptersDir(root); err == nil || got != "" {
		t.Fatalf("gatepostAdaptersDir with group-writable tool dir = %q, %v; want empty with error", got, err)
	}
}

func TestGatepostAdaptersDirRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	escape := t.TempDir()
	for _, base := range []string{root, escape} {
		for _, rel := range []string{
			filepath.Join("adapters", "claude", "gatepost-events.py"),
			filepath.Join("adapters", "codex", "gatepost-events.py"),
		} {
			path := filepath.Join(base, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("#!/usr/bin/env python3\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.RemoveAll(filepath.Join(root, "adapters", "claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(escape, "adapters", "claude"), filepath.Join(root, "adapters", "claude")); err != nil {
		t.Fatal(err)
	}
	if got, err := gatepostAdaptersDir(root); err == nil || got != "" {
		t.Fatalf("gatepostAdaptersDir with symlink escape = %q, %v; want empty with error", got, err)
	}
}

func TestGatepostCleanupStrictReportsFailures(t *testing.T) {
	g := &GatepostTarget{}
	meta := session.TargetMeta{ContainerName: "agent", NetworkName: "internal", Gatepost: session.GatepostMeta{ProxyContainerName: "proxy"}}
	err := g.cleanupWithRunner(context.Background(), meta, func(args ...string) error {
		if len(args) > 0 && args[0] == "rm" {
			return nil
		}
		return os.ErrPermission
	})
	if err == nil {
		t.Fatal("cleanupWithRunner returned nil; want propagated cleanup error")
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

func TestRemoveGatepostRuntimeStateRemovesSessionDir(t *testing.T) {
	dir := t.TempDir()
	r := gatepostRuntime{sessionDir: filepath.Join(dir, "session")}
	if err := os.MkdirAll(filepath.Join(r.sessionDir, "agent-home"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeGatepostRuntimeState(r); err != nil {
		t.Fatalf("removeGatepostRuntimeState: %v", err)
	}
	if _, err := os.Stat(r.sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session dir still exists or unexpected stat error: %v", err)
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
