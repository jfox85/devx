package main

import (
	"os"
	"os/exec"
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

// A customised GOPATH must be honoured. Assuming ~/go/bin meant the desktop
// shell never saw a `make install` build under e.g. GOPATH=~/projects/go, and
// fell through to an older devx elsewhere on PATH — which silently stripped
// "pinned" from every session on each add/remove.
func TestGoBinDirs_HonoursCustomGOPATH(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "projects", "go")
	t.Setenv("GOPATH", custom)
	t.Setenv("GOBIN", "")

	dirs := goBinDirs(home)

	want := filepath.Join(custom, "bin")
	if !containsPath(dirs, want) {
		t.Errorf("custom GOPATH bin %q missing from %v", want, dirs)
	}
}

// GOBIN overrides GOPATH/bin for `go install`, so it must be preferred.
func TestGoBinDirs_PrefersGOBIN(t *testing.T) {
	home := t.TempDir()
	gobin := filepath.Join(home, "custom", "gobin")
	t.Setenv("GOBIN", gobin)
	t.Setenv("GOPATH", filepath.Join(home, "projects", "go"))

	dirs := goBinDirs(home)

	if len(dirs) == 0 || dirs[0] != gobin {
		t.Errorf("GOBIN %q should be first, got %v", gobin, dirs)
	}
}

// With no Go environment at all we still fall back to the documented default
// so behaviour is unchanged for users without a custom GOPATH.
func TestGoBinDirs_FallsBackToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	t.Setenv("PATH", "") // hide the go toolchain where possible

	dirs := goBinDirs(home)

	if !containsPath(dirs, filepath.Join(home, "go", "bin")) {
		t.Errorf("default ~/go/bin missing from %v", dirs)
	}
}

// Go bin dirs must precede /usr/local/bin so a current developer build wins
// over an older system-wide install.
func TestCandidateBinDirs_GoBinBeatsUsrLocalBin(t *testing.T) {
	home := t.TempDir()
	custom := filepath.Join(home, "projects", "go")
	t.Setenv("GOPATH", custom)
	t.Setenv("GOBIN", "")

	dirs := candidateBinDirs()

	goIdx, usrIdx := -1, -1
	for i, d := range dirs {
		if d == filepath.Join(custom, "bin") && goIdx == -1 {
			goIdx = i
		}
		if d == "/usr/local/bin" && usrIdx == -1 {
			usrIdx = i
		}
	}
	if goIdx == -1 {
		t.Fatalf("GOPATH bin missing from %v", dirs)
	}
	if usrIdx == -1 {
		t.Fatalf("/usr/local/bin missing from %v", dirs)
	}
	if goIdx > usrIdx {
		t.Errorf("GOPATH bin (%d) must precede /usr/local/bin (%d): %v", goIdx, usrIdx, dirs)
	}
}

// `go env GOBIN GOPATH` prints an empty line for an unset GOBIN. Trimming the
// whole output before splitting swallowed that blank line and shifted GOPATH's
// value into the GOBIN slot, producing a bogus "<gopath>" dir (no /bin) and
// losing the real "<gopath>/bin". Assert the real toolchain is parsed
// positionally.
func TestGoEnvBinPaths_DoesNotShiftGOPATHIntoGOBIN(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	// `cfg.Getenv` falls back to the persisted go env file when the OS variable
	// is empty, so a developer with a saved GOBIN would get a non-empty first
	// line and this test would not exercise the blank-line case at all.
	// GOENV=off disables that file so GOBIN is genuinely reported as empty.
	t.Setenv("GOENV", "off")
	t.Setenv("GOBIN", "")

	gobin, gopath := goEnvBinPaths()

	if gobin != "" {
		t.Fatalf("expected an empty GOBIN with GOENV=off, got %q", gobin)
	}
	if gopath == "" {
		t.Fatalf("expected a GOPATH from the toolchain, got gobin=%q gopath=%q", gobin, gopath)
	}
	if gobin == gopath {
		t.Errorf("GOPATH %q leaked into the GOBIN slot", gobin)
	}
	// The GOPATH bin dir must end in /bin; a shifted parse loses that suffix.
	dirs := goBinDirs(t.TempDir())
	want := filepath.Join(gopath, "bin")
	if !containsPath(dirs, want) {
		t.Errorf("GOPATH bin %q missing from %v", want, dirs)
	}
	if containsPath(dirs, gopath) {
		t.Errorf("bare GOPATH %q (missing /bin) leaked into %v", gopath, dirs)
	}
}

// An empty GOPATH list element (leading, trailing, or doubled separator) joins
// to the RELATIVE path "bin". Prepending that to PATH makes tool resolution
// depend on the working directory, so a stray ./bin/devx could shadow the real
// CLI. Every candidate must be absolute.
func TestGoBinDirs_SkipsEmptyGOPATHEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", string(os.PathListSeparator)+filepath.Join(home, "projects", "go")+string(os.PathListSeparator))

	for _, dir := range goBinDirs(home) {
		if !filepath.IsAbs(dir) {
			t.Errorf("relative candidate %q from malformed GOPATH", dir)
		}
	}
}

// augmentPATH is where PATH is actually mutated, so it must reject relative
// entries even if a candidate slipped through.
func TestAugmentPATH_NeverPrependsRelativeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", string(os.PathListSeparator)+filepath.Join(home, "projects", "go"))

	// A real ./bin in the working directory would otherwise pass the Stat check.
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, "bin"), 0o755); err != nil {
		t.Fatalf("create ./bin fixture: %v", err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(work); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	t.Setenv("PATH", "/usr/bin")
	augmentPATH()

	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p != "" && !filepath.IsAbs(p) {
			t.Errorf("relative entry %q reached PATH: %s", p, os.Getenv("PATH"))
		}
	}
}

func containsPath(dirs []string, want string) bool {
	for _, d := range dirs {
		if d == want {
			return true
		}
	}
	return false
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
