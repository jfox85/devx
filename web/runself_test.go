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
