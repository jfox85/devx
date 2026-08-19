package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAugmentPATH_PrependsExistingDirsWithoutDuplicates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// A real candidate dir (Go bin) and a Python user-base dir.
	goBin := filepath.Join(home, "go", "bin")
	pyBin := filepath.Join(home, "Library", "Python", "3.11", "bin")
	for _, d := range []string{goBin, pyBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Start with a minimal PATH that already contains goBin (must not duplicate).
	t.Setenv("PATH", "/usr/bin"+string(os.PathListSeparator)+goBin)

	augmentPATH()

	got := filepath.SplitList(os.Getenv("PATH"))
	count := map[string]int{}
	for _, p := range got {
		count[p]++
	}
	if count[goBin] != 1 {
		t.Errorf("goBin should appear exactly once, got %d in %v", count[goBin], got)
	}
	if count[pyBin] != 1 {
		t.Errorf("pyBin should be added, got %d in %v", count[pyBin], got)
	}
	if !strings.Contains(os.Getenv("PATH"), "/usr/bin") {
		t.Errorf("existing /usr/bin should be preserved: %q", os.Getenv("PATH"))
	}
}

func TestAugmentPATH_SkipsMissingDirs(t *testing.T) {
	home := t.TempDir() // empty: no go/bin, no Library/Python
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin")

	augmentPATH()

	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.HasPrefix(p, home) {
			t.Errorf("no nonexistent home dir should be added, got %q", p)
		}
	}
}
