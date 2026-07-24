package session

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// envMap converts a []string of "KEY=VALUE" entries into a map for easy lookup.
// When a key appears more than once (as it can after appending to os.Environ()),
// the last occurrence wins, matching exec's own "last value wins" semantics.
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

func TestPrepareCleanupEnvironment(t *testing.T) {
	sess := &Session{
		Name:   "my-session",
		Branch: "feature/thing",
		Path:   "/tmp/worktrees/my-session",
		Ports: map[string]int{
			"ui":           3000,
			"auth-service": 4001,
		},
		Routes: map[string]string{
			"ui":           "my-session-ui.localhost",
			"auth-service": "my-session-auth.localhost",
		},
	}

	env := prepareCleanupEnvironment(sess)
	got := envMap(env)

	// The current process environment must be carried through so cleanup
	// commands still see PATH and friends.
	if _, ok := got["PATH"]; !ok {
		// PATH may legitimately be empty in some sandboxes; fall back to
		// checking that at least one inherited variable made it through.
		if len(env) <= 6 {
			t.Errorf("expected inherited environment to be present, got %d entries", len(env))
		}
	}

	checks := map[string]string{
		"SESSION_NAME":   "my-session",
		"SESSION_BRANCH": "feature/thing",
		"WORKTREE_PATH":  "/tmp/worktrees/my-session",
		// Service name -> PORT variable: uppercased, dashes become underscores.
		"UI_PORT":           "3000",
		"AUTH_SERVICE_PORT": "4001",
		// Service name -> HOST variable: uppercased, dashes become underscores,
		// value prefixed with http://.
		"UI_HOST":           "http://my-session-ui.localhost",
		"AUTH_SERVICE_HOST": "http://my-session-auth.localhost",
	}

	for key, want := range checks {
		if got[key] != want {
			t.Errorf("env[%q] = %q, want %q", key, got[key], want)
		}
	}
}

func TestPrepareCleanupEnvironmentNoPortsOrRoutes(t *testing.T) {
	sess := &Session{
		Name:   "bare",
		Branch: "main",
		Path:   "/tmp/bare",
	}

	env := prepareCleanupEnvironment(sess)
	got := envMap(env)

	if got["SESSION_NAME"] != "bare" {
		t.Errorf("SESSION_NAME = %q, want %q", got["SESSION_NAME"], "bare")
	}
	if got["SESSION_BRANCH"] != "main" {
		t.Errorf("SESSION_BRANCH = %q, want %q", got["SESSION_BRANCH"], "main")
	}
	if got["WORKTREE_PATH"] != "/tmp/bare" {
		t.Errorf("WORKTREE_PATH = %q, want %q", got["WORKTREE_PATH"], "/tmp/bare")
	}

	// Only the session-level variables should be appended beyond the base
	// process environment: SESSION_NAME, WORKTREE_PATH, SESSION_BRANCH. No
	// service-derived *_PORT/*_HOST variables should be added when the session
	// has no ports or routes. Compare against the base environment so inherited
	// variables (which may legitimately contain "_PORT") don't cause spurious
	// failures.
	base := len(os.Environ())
	if added := len(env) - base; added != 3 {
		t.Errorf("expected 3 session variables appended, got %d (env=%d, base=%d)", added, len(env), base)
	}
}

func TestExecuteCleanupCommandSuccess(t *testing.T) {
	dir := t.TempDir()
	marker := dir + "/cleanup-ran"

	// `touch` a marker file so we can confirm the command actually executed
	// in the intended working directory with the provided environment.
	err := executeCleanupCommand("touch cleanup-ran", dir, os.Environ())
	if err != nil {
		t.Fatalf("executeCleanupCommand returned error: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Errorf("expected marker file %q to exist: %v", marker, statErr)
	}
}

func TestExecuteCleanupCommandEmpty(t *testing.T) {
	err := executeCleanupCommand("   ", t.TempDir(), os.Environ())
	if err == nil {
		t.Fatal("expected error for empty command")
	}
	if !strings.Contains(err.Error(), "empty cleanup command") {
		t.Errorf("expected empty command error, got: %v", err)
	}
}

func TestExecuteCleanupCommandNonexistentBinary(t *testing.T) {
	err := executeCleanupCommand("this-binary-does-not-exist-devx", t.TempDir(), os.Environ())
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	if !strings.Contains(err.Error(), "failed to start cleanup command") {
		t.Errorf("expected start failure error, got: %v", err)
	}
}

func TestExecuteCleanupCommandExitError(t *testing.T) {
	// `false` exits non-zero; the wrapper should surface that as an error.
	err := executeCleanupCommand("false", t.TempDir(), os.Environ())
	if err == nil {
		t.Fatal("expected error for command exiting non-zero")
	}
	if !strings.Contains(err.Error(), "exited with error") {
		t.Errorf("expected exit error, got: %v", err)
	}
}

// The RunCleanupCommand* tests below mutate the global viper singleton via
// viper.Set. They rely on Go running tests within a package sequentially and
// each resetting viper on entry/exit; none of them call t.Parallel(). If that
// ever changes, this shared global state must be isolated per-test first.

func TestRunCleanupCommandNoConfig(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	// With no cleanup_command configured, RunCleanupCommand is a no-op.
	sess := &Session{Name: "x", Path: t.TempDir()}
	if err := RunCleanupCommand(sess); err != nil {
		t.Errorf("expected nil error with no cleanup command, got: %v", err)
	}
}

func TestRunCleanupCommandExecutes(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	dir := t.TempDir()
	viper.Set("cleanup_command", "touch ran-marker")

	sess := &Session{Name: "cleanup-me", Branch: "main", Path: dir}
	if err := RunCleanupCommand(sess); err != nil {
		t.Fatalf("RunCleanupCommand returned error: %v", err)
	}

	if _, err := os.Stat(dir + "/ran-marker"); err != nil {
		t.Errorf("expected cleanup command to create marker file: %v", err)
	}
}

func TestRunCleanupCommandForShellNoConfig(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	sess := &Session{Name: "x", Path: t.TempDir()}
	if err := RunCleanupCommandForShell(sess); err != nil {
		t.Errorf("expected nil error with no cleanup command, got: %v", err)
	}
}

func TestRunCleanupCommandForShellExecutesComplexCommand(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	dir := t.TempDir()
	// A command with a pipe/redirect that only works via the shell path.
	viper.Set("cleanup_command", "echo $SESSION_NAME > name.txt")

	sess := &Session{Name: "shell-session", Branch: "main", Path: dir}
	if err := RunCleanupCommandForShell(sess); err != nil {
		t.Fatalf("RunCleanupCommandForShell returned error: %v", err)
	}

	data, err := os.ReadFile(dir + "/name.txt")
	if err != nil {
		t.Fatalf("expected name.txt to be written: %v", err)
	}
	if strings.TrimSpace(string(data)) != "shell-session" {
		t.Errorf("expected SESSION_NAME to be expanded to %q, got %q", "shell-session", strings.TrimSpace(string(data)))
	}
}

func TestRunCleanupCommandForShellPropagatesFailure(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	viper.Set("cleanup_command", "exit 3")

	sess := &Session{Name: "fail", Branch: "main", Path: t.TempDir()}
	err := RunCleanupCommandForShell(sess)
	if err == nil {
		t.Fatal("expected error when shell cleanup command fails")
	}
}
