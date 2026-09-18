package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// augmentPATH prepends common user/tool bin directories to PATH so the embedded
// DevX server can locate CLI tools (tmuxp, tmux, ttyd, direnv, devx, ...) when
// the app is launched from the macOS GUI (Finder/Dock/Spotlight). GUI processes
// inherit only a minimal PATH, unlike a terminal-launched `devx web`.
//
// Only directories that exist and are not already present are added, so this is
// a no-op when launched from a shell with a full PATH.
func augmentPATH() {
	existing := os.Getenv("PATH")
	present := map[string]bool{}
	for _, p := range filepath.SplitList(existing) {
		if p != "" {
			present[p] = true
		}
	}

	var add []string
	consider := func(dir string) {
		if dir == "" || present[dir] {
			return
		}
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			present[dir] = true
			add = append(add, dir)
		}
	}

	for _, dir := range candidateBinDirs() {
		consider(dir)
	}

	if len(add) == 0 {
		return
	}
	joined := strings.Join(add, string(os.PathListSeparator))
	if existing != "" {
		joined += string(os.PathListSeparator) + existing
	}
	os.Setenv("PATH", joined)
}

// candidateBinDirs returns user/tool bin directories to prepend, most-specific
// first. Python user-scripts dirs (where `pip install --user tmuxp` lands) are
// resolved dynamically because they are version-stamped (e.g. 3.11).
//
// The Go bin dir is resolved from the toolchain rather than assumed to be
// ~/go/bin: a customised GOPATH/GOBIN (e.g. GOPATH=~/projects/go) puts
// `make install` output somewhere ~/go/bin never covers. Getting this wrong is
// not merely "tool not found" — the desktop shell would fall through to an
// older `devx` elsewhere on PATH, and because every sessions.json mutation is a
// full read-modify-write, that stale CLI silently strips fields it predates
// (e.g. "pinned") from every session.
//
// Go bin dirs come first so a current developer build wins over an older
// /usr/local/bin install.
func candidateBinDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := goBinDirs(home)
	dirs = append(dirs,
		"/opt/homebrew/bin", // Apple Silicon Homebrew
		"/usr/local/bin",    // Intel Homebrew / common installs
	)
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".local", "bin"),
		)
		dirs = append(dirs, pythonUserBinDirs(home)...)
	}
	return dirs
}

// goBinDirs returns the directories `go install` writes to, most-specific
// first: GOBIN when set, otherwise <GOPATH>/bin for each GOPATH entry.
//
// GUI-launched apps inherit neither the user's shell environment nor its PATH,
// so the GOPATH/GOBIN env vars are usually absent here. `go env` is consulted
// to recover the real values, falling back to the ~/go/bin default when the Go
// toolchain is not installed or not reachable.
func goBinDirs(home string) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}

	if gobin := strings.TrimSpace(os.Getenv("GOBIN")); gobin != "" {
		add(gobin)
	}
	for _, gopath := range filepath.SplitList(os.Getenv("GOPATH")) {
		add(filepath.Join(gopath, "bin"))
	}

	// Ask the toolchain when the environment did not tell us. `go env` reads the
	// persisted go/env config too, so this recovers values a GUI launch drops.
	if gobin, gopath := goEnvBinPaths(); gobin != "" || gopath != "" {
		add(gobin)
		for _, p := range filepath.SplitList(gopath) {
			add(filepath.Join(p, "bin"))
		}
	}

	// Default GOPATH when nothing else resolved it.
	if home != "" {
		add(filepath.Join(home, "go", "bin"))
	}
	return dirs
}

// goEnvBinPaths shells out to `go env` for GOBIN and GOPATH. It returns empty
// strings when the Go toolchain is unavailable, which is the common case for a
// user running a released build rather than a developer install.
func goEnvBinPaths() (gobin, gopath string) {
	goCmd, lookErr := exec.LookPath("go")
	if lookErr != nil {
		// A GUI launch may not see `go` on PATH at all. Probe the usual install
		// locations before giving up. LookPath leaves goCmd empty on error, so it
		// stays empty when no candidate matches.
		goCmd = ""
		for _, candidate := range []string{"/opt/homebrew/bin/go", "/usr/local/go/bin/go", "/usr/local/bin/go"} {
			if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
				goCmd = candidate
				break
			}
		}
		if goCmd == "" {
			return "", ""
		}
	}
	out, err := exec.Command(goCmd, "env", "GOBIN", "GOPATH").Output()
	if err != nil {
		return "", ""
	}
	// `go env` prints one line per requested variable, in order, and prints an
	// EMPTY line for an unset one (GOBIN is usually unset). Trimming the whole
	// output first would swallow that leading blank line and shift GOPATH's value
	// into the GOBIN slot, yielding a bogus "<gopath>" bin dir and losing the real
	// "<gopath>/bin". Split first, then trim each line positionally.
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > 0 {
		gobin = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		gopath = strings.TrimSpace(lines[1])
	}
	return gobin, gopath
}

// pythonUserBinDirs finds versioned Python user-base script directories such as
// ~/Library/Python/3.11/bin (macOS) and ~/.local/bin is handled separately.
func pythonUserBinDirs(home string) []string {
	base := filepath.Join(home, "Library", "Python")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(base, e.Name(), "bin"))
		}
	}
	return dirs
}
