package web

import (
	"os"
	"path/filepath"
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
	cli := filepath.Join(dir, "devx")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := runSelfExecutable("/Applications/DevX.app/Contents/MacOS/devx-desktop"); got != cli {
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
	bundled := filepath.Join(appDir, "devx")
	if err := os.WriteFile(bundled, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bundled devx CLI: %v", err)
	}

	// A different (stale) devx earlier on PATH must lose to the bundled one.
	pathDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(pathDir, "devx"), []byte("#!/bin/sh\n"), 0o755); err != nil {
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
	cli := filepath.Join(pathDir, "devx")
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
	t.Setenv("DEVX_CLI_BINARY", "")

	appDir := t.TempDir()
	desktop := filepath.Join(appDir, "devx-desktop")
	if err := os.WriteFile(filepath.Join(appDir, "devx"), []byte("not executable"), 0o644); err != nil {
		t.Fatalf("write non-executable sibling: %v", err)
	}

	pathDir := t.TempDir()
	cli := filepath.Join(pathDir, "devx")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write PATH devx CLI: %v", err)
	}
	t.Setenv("PATH", pathDir)

	if got := runSelfExecutable(desktop); got != cli {
		t.Fatalf("non-executable sibling should be ignored, got %q want %q", got, cli)
	}
}
