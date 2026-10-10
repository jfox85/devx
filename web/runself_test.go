package web

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRunSelfExecutableUsesOverride(t *testing.T) {
	t.Setenv("DEVX_CLI_BINARY", "/tmp/devx-current")
	if got := runSelfExecutable("/Applications/DevX.app/Contents/MacOS/devx-desktop"); got != "/tmp/devx-current" {
		t.Fatalf("runSelfExecutable override = %q", got)
	}
}

func TestRunSelfExecutableDesktopPrefersDevxOnPath(t *testing.T) {
	t.Setenv("DEVX_CLI_BINARY", "")
	dir := t.TempDir()
	cli := filepath.Join(dir, cliExecutableName)
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write PATH devx CLI: %v", err)
	}
	t.Setenv("PATH", dir)
	// Use a temp app dir with no sibling CLI rather than the literal
	// /Applications path: on a machine with DevX.app actually installed, the
	// bundled sibling exists and (correctly) wins, so a hardcoded real path made
	// this test depend on local filesystem state and pass only in CI.
	desktop := filepath.Join(t.TempDir(), "devx-desktop")
	if got := runSelfExecutable(desktop); got != cli {
		t.Fatalf("desktop runSelfExecutable = %q, want %q", got, cli)
	}
}

func TestRunSelfExecutableNonDesktopUsesCurrent(t *testing.T) {
	t.Setenv("DEVX_CLI_BINARY", "")
	if got := runSelfExecutable("/usr/local/bin/devx"); got != "/usr/local/bin/devx" {
		t.Fatalf("non-desktop runSelfExecutable = %q", got)
	}
}

// A devx CLI bundled next to the desktop binary is built from the same source
// tree, so it must win over whatever `devx` happens to be on PATH. Picking a
// stale PATH install is what silently stripped "pinned" from every session:
// each sessions.json mutation is a full read-modify-write, so an older CLI
// drops fields it does not know about.
func TestRunSelfExecutableDesktopPrefersBundledCLIOverPath(t *testing.T) {
	t.Setenv("DEVX_CLI_BINARY", "")

	appDir := t.TempDir()
	desktop := filepath.Join(appDir, "devx-desktop")
	bundled := filepath.Join(appDir, cliExecutableName)
	if err := os.WriteFile(bundled, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bundled devx CLI: %v", err)
	}

	// A different (stale) devx earlier on PATH must lose to the bundled one.
	pathDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathDir, cliExecutableName), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write stale PATH devx CLI: %v", err)
	}
	t.Setenv("PATH", pathDir)

	if got := runSelfExecutable(desktop); got != bundled {
		t.Fatalf("desktop runSelfExecutable = %q, want bundled %q", got, bundled)
	}
}

// Without a bundled CLI the PATH lookup remains the fallback, so existing
// installs keep working.
func TestRunSelfExecutableDesktopFallsBackToPathWithoutBundledCLI(t *testing.T) {
	t.Setenv("DEVX_CLI_BINARY", "")

	appDir := t.TempDir() // no sibling devx
	desktop := filepath.Join(appDir, "devx-desktop")

	pathDir := t.TempDir()
	cli := filepath.Join(pathDir, cliExecutableName)
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write PATH devx CLI: %v", err)
	}
	t.Setenv("PATH", pathDir)

	if got := runSelfExecutable(desktop); got != cli {
		t.Fatalf("desktop runSelfExecutable = %q, want PATH %q", got, cli)
	}
}

// A non-executable or non-regular sibling must not be mistaken for a CLI.
func TestRunSelfExecutableIgnoresNonExecutableSibling(t *testing.T) {
	t.Run("non-regular", func(t *testing.T) {
		assertSiblingIgnored(t, func(sibling string) error { return os.Mkdir(sibling, 0o755) })
	})
	t.Run("no exec bits", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows has no execute permission bits; runnability comes from the .exe name")
		}
		assertSiblingIgnored(t, func(sibling string) error {
			return os.WriteFile(sibling, []byte("not executable"), 0o644)
		})
	})
}

func assertSiblingIgnored(t *testing.T, makeSibling func(string) error) {
	t.Helper()
	t.Setenv("DEVX_CLI_BINARY", "")

	appDir := t.TempDir()
	desktop := filepath.Join(appDir, "devx-desktop")
	if err := makeSibling(filepath.Join(appDir, cliExecutableName)); err != nil {
		t.Fatalf("create sibling: %v", err)
	}

	pathDir := t.TempDir()
	cli := filepath.Join(pathDir, cliExecutableName)
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write PATH devx CLI: %v", err)
	}
	t.Setenv("PATH", pathDir)

	if got := runSelfExecutable(desktop); got != cli {
		t.Fatalf("non-executable sibling should be ignored, got %q want %q", got, cli)
	}
}
