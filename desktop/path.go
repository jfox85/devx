package main

import (
	"os"
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
func candidateBinDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{
		"/opt/homebrew/bin", // Apple Silicon Homebrew
		"/usr/local/bin",    // Intel Homebrew / common installs
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".local", "bin"),
		)
		dirs = append(dirs, pythonUserBinDirs(home)...)
	}
	return dirs
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
