package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestOpenCommandRevealsUnsafeFiles(t *testing.T) {
	cases := []struct {
		goos, path string
		want       []string
	}{
		{"darwin", "/a/shot.png", []string{"open", "--", "/a/shot.png"}},
		{"darwin", "/a/run.command", []string{"open", "-R", "--", "/a/run.command"}},
		{"linux", "/a/r.html", []string{"xdg-open", "/a/r.html"}},
		{"linux", "/a/x.sh", []string{"xdg-open", "/a"}},
		{"windows", `C:\a\shot.png`, []string{"explorer.exe", `C:\a\shot.png`}},
		{"windows", `C:\a\run.bat`, []string{"explorer.exe", `/select,C:\a\run.bat`}},
	}
	for _, c := range cases {
		cmd, err := openCommand(c.goos, c.path)
		if err != nil {
			t.Fatalf("%s %s: %v", c.goos, c.path, err)
		}
		if !slices.Equal(cmd.Args, c.want) {
			t.Errorf("%s %s: args %v, want %v", c.goos, c.path, cmd.Args, c.want)
		}
	}
	if _, err := openCommand("plan9", "/a/b.png"); err == nil {
		t.Fatal("expected unsupported OS error")
	}
}

func TestCopyArtifactFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.png")
	if err := os.WriteFile(src, []byte("img"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out", "saved.png")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyArtifactFile(src, dest); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "img" {
		t.Fatalf("dest = %q", data)
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Fatalf("expected no temp files left, got %d entries", len(entries))
	}

	// Overwriting a file the user chose in the Save dialog must succeed on
	// every platform (os.Rename replaces an existing file, Windows included).
	if err := os.WriteFile(src, []byte("img2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyArtifactFile(src, dest); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "img2" {
		t.Fatalf("overwritten dest = %q", data)
	}

	if runtime.GOOS == "windows" {
		return // creating symlinks needs privileges; O_NOFOLLOW does not exist there
	}

	link := filepath.Join(dir, "link.png")
	if err := os.Symlink(src, link); err != nil {
		t.Fatal(err)
	}
	if err := copyArtifactFile(link, filepath.Join(dir, "out", "x.png")); err == nil {
		t.Fatal("expected symlink source to be rejected")
	}
}
