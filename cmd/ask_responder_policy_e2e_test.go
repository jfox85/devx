package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAskResponderIgnoresProjectConfig drives the real devx binary: a project
// .devx/config.yaml that enables the responder in "always" mode with its own
// command must not run anything. Only the owner's config decides. Fixture
// only: fake HOME, a synthetic repo, and tripwire/recorder scripts.
func TestAskResponderIgnoresProjectConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell scripts as responder executables")
	}
	if os.Getenv("DEVX_CLI_E2E") == "0" {
		t.Skip("DEVX_CLI_E2E=0")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repoRoot, _ := filepath.Abs("..")
	bin := filepath.Join(home, "devx")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = repoRoot
	build.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build devx: %v\n%s", err, out)
	}
	trip := filepath.Join(home, "tripwire.log")
	repoCmd := filepath.Join(home, "repo-responder")
	_ = os.WriteFile(repoCmd, []byte(fmt.Sprintf("#!/bin/sh\necho \"repo $*\" >> %s\necho pwned\n", trip)), 0o700)
	record := filepath.Join(home, "owner.log")
	ownerCmd := filepath.Join(home, "owner-responder")
	_ = os.WriteFile(ownerCmd, []byte(fmt.Sprintf("#!/bin/sh\necho \"owner $*\" >> %s\necho owner-answer\n", record)), 0o700)

	// Two sessions: the asker and a target whose worktree is a repository
	// that tries to take over the responder.
	target := filepath.Join(home, "target")
	asker := filepath.Join(home, "asker")
	for _, d := range []string{target, asker} {
		mustRun(t, "", "git", "init", "-q", "-b", "main", d)
	}
	projCfg := fmt.Sprintf("agent_responder:\n  enabled: true\n  mode: always\n  command: %s\n  args: [--repo]\n", repoCmd)
	for _, d := range []string{target, asker} {
		_ = os.MkdirAll(filepath.Join(d, ".devx"), 0o755)
		_ = os.WriteFile(filepath.Join(d, ".devx", "config.yaml"), []byte(projCfg), 0o644)
	}
	cfgDir := filepath.Join(home, ".config", "devx")
	_ = os.MkdirAll(cfgDir, 0o700)
	sessions := fmt.Sprintf(`{"sessions":{"asker":{"name":"asker","branch":"main","path":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"},"target":{"name":"target","branch":"main","path":%q,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}}}`, asker, target)
	_ = os.WriteFile(filepath.Join(cfgDir, "sessions.json"), []byte(sessions), 0o600)
	base := "disable_caddy: true\nauto_check_updates: false\nweb_autostart: false\nusage:\n  enabled: false\n"
	writeOwner := func(body string) {
		if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(base+body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "DEVX_DISABLE_CADDY=true",
		"DEVX_AGENT_RESPONDER.ENABLED=true", "DEVX_AGENT_RESPONDER_ENABLED=true", "DEVX_AGENT_RESPONDER_MODE=always")
	askIn := func(dir string) map[string]any {
		t.Helper()
		c := exec.Command(bin, "ask", "--json", "target", "what changed?")
		c.Dir = dir
		c.Env = env
		out, _ := c.CombinedOutput()
		s := string(out)
		i := strings.Index(s, "{")
		var r map[string]any
		if i < 0 || json.Unmarshal([]byte(s[i:]), &r) != nil {
			t.Fatalf("ask output: %s", s)
		}
		return r
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "session")); err != nil {
		t.Skip("not in the devx repo")
	}

	// 1. Owner leaves the responder disabled: the repository cannot enable it.
	writeOwner("")
	if r := askIn(asker); r["status"] != "pending_approval" {
		t.Fatalf("responder ran or failed although the owner disabled it: %v", r)
	}
	if _, err := os.Stat(trip); err == nil {
		b, _ := os.ReadFile(trip)
		t.Fatalf("repository responder ran: %s", b)
	}
	// 2. Owner enables "always" with its own command: the owner's runs, never the repository's.
	writeOwner(fmt.Sprintf("agent_responder:\n  enabled: true\n  mode: always\n  command: %s\n  args: [--owner]\n", ownerCmd))
	r := askIn(asker)
	var b []byte
	b, err = os.ReadFile(record)
	if err != nil || !strings.Contains(string(b), "owner --owner") {
		t.Fatalf("owner responder did not run with owner args: %q %v (resp %v)", b, err, r)
	}
	if _, err := os.Stat(trip); err == nil {
		b, _ := os.ReadFile(trip)
		t.Fatalf("repository responder ran: %s", b)
	}
	// 3. Unusable owner config: fail closed (nothing runs).
	_ = os.Remove(record)
	writeOwner("agent_responder:\n  enabled: true\n  mode: always\n  shell: yes\n")
	if r := askIn(asker); r["status"] != "pending_approval" {
		t.Fatalf("invalid owner config did not fail closed: %v", r)
	}
	if _, err := os.Stat(record); err == nil {
		t.Fatal("responder ran with an invalid owner config")
	}
	if _, err := os.Stat(trip); err == nil {
		t.Fatal("repository responder ran with an invalid owner config")
	}
}
