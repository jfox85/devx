package update

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsHomebrewManagedCellarPaths verifies the substring-based detection for
// direct Cellar installations on both Intel (/usr/local) and Apple Silicon
// (/opt/homebrew) layouts.
func TestIsHomebrewManagedCellarPaths(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"intel cellar", "/usr/local/Cellar/devx/1.2.3/bin/devx", true},
		{"apple silicon cellar", "/opt/homebrew/Cellar/devx/1.2.3/bin/devx", true},
		{"unrelated cellar package", "/opt/homebrew/Cellar/ripgrep/14.0/bin/rg", false},
		// Sibling package sharing the "devx" prefix must not collide: the package
		// name has to occupy a full path component, not just match as a substring.
		{"cellar package with devx prefix", "/opt/homebrew/Cellar/devx-tools/1.2.3/bin/devx", false},
		{"plain manual path", "/usr/local/custom/devx", false},
		{"go bin path", "/home/user/go/bin/devx", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHomebrewManaged(tc.path); got != tc.want {
				t.Errorf("isHomebrewManaged(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestIsHomebrewManagedSymlinkToCellar verifies that a Homebrew-style bin
// symlink pointing into a Cellar directory is detected as Homebrew-managed.
func TestIsHomebrewManagedSymlinkToCellar(t *testing.T) {
	dir := t.TempDir()

	cellar := filepath.Join(dir, "Cellar", "devx", "1.0.0", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatalf("failed to create cellar dir: %v", err)
	}
	realBin := filepath.Join(cellar, "devx")
	if err := os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("failed to write real bin: %v", err)
	}

	link := filepath.Join(dir, "devx")
	if err := os.Symlink(realBin, link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	if !isHomebrewManaged(link) {
		t.Errorf("expected symlink pointing into Cellar to be detected as Homebrew-managed")
	}
}

// TestIsHomebrewManagedSymlinkToSiblingPackage verifies that a symlink resolving
// into a sibling Cellar package that merely shares the "devx" prefix (e.g.
// "devx-tools") is not treated as a devx Homebrew install.
func TestIsHomebrewManagedSymlinkToSiblingPackage(t *testing.T) {
	dir := t.TempDir()

	cellar := filepath.Join(dir, "Cellar", "devx-tools", "1.0.0", "bin")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatalf("failed to create cellar dir: %v", err)
	}
	realBin := filepath.Join(cellar, "devx")
	if err := os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("failed to write real bin: %v", err)
	}

	link := filepath.Join(dir, "devx")
	if err := os.Symlink(realBin, link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	if isHomebrewManaged(link) {
		t.Errorf("expected symlink into sibling package 'devx-tools' to not be Homebrew-managed")
	}
}

// TestIsHomebrewManagedSymlinkOutsideCellar verifies that a symlink to a
// non-Cellar location is not treated as Homebrew-managed.
func TestIsHomebrewManagedSymlinkOutsideCellar(t *testing.T) {
	dir := t.TempDir()

	realBin := filepath.Join(dir, "actual-devx")
	if err := os.WriteFile(realBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("failed to write real bin: %v", err)
	}

	link := filepath.Join(dir, "devx")
	if err := os.Symlink(realBin, link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	if isHomebrewManaged(link) {
		t.Errorf("expected symlink outside Cellar to not be Homebrew-managed")
	}
}

// TestIsHomebrewManagedPlainFile verifies a regular (non-symlink) file at a
// path that isn't a Cellar path is not Homebrew-managed.
func TestIsHomebrewManagedPlainFile(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "devx")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("failed to write bin: %v", err)
	}

	if isHomebrewManaged(bin) {
		t.Errorf("expected plain file to not be Homebrew-managed")
	}
}
