//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfox85/devx/web/imagepolicy"
)

// writeTemp writes n bytes of 0xAB to a file in dir and returns its path.
func writeTemp(t *testing.T, dir, name string, n int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(strings.Repeat("\xAB", n)), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestReadDroppedImage_RegularFile(t *testing.T) {
	dir := t.TempDir()
	p := writeTemp(t, dir, "ok.png", 1024)

	data, err := readDroppedImage(p)
	if err != nil {
		t.Fatalf("readDroppedImage: %v", err)
	}
	if len(data) != 1024 {
		t.Fatalf("got %d bytes, want 1024", len(data))
	}
}

func TestReadDroppedImage_RejectsSymlinkLeaf(t *testing.T) {
	dir := t.TempDir()
	target := writeTemp(t, dir, "secret", 16)
	link := filepath.Join(dir, "evil.png")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// O_NOFOLLOW must refuse to dereference a symlinked final component so a
	// dropped ".png" symlink can't exfiltrate an arbitrary readable file.
	if _, err := readDroppedImage(link); err == nil {
		t.Fatal("expected error for symlinked leaf, got nil")
	}
}

func TestReadDroppedImage_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "adir")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := readDroppedImage(sub); err == nil {
		t.Fatal("expected error for non-regular file, got nil")
	}
}

func TestReadDroppedImage_EnforcesSizeCap(t *testing.T) {
	dir := t.TempDir()
	// One byte over the cap must be rejected.
	p := writeTemp(t, dir, "big.png", imagepolicy.MaxUploadBytes+1)

	if _, err := readDroppedImage(p); err == nil {
		t.Fatal("expected error for oversize file, got nil")
	}

	// Exactly at the cap is allowed.
	p = writeTemp(t, dir, "atcap.png", imagepolicy.MaxUploadBytes)
	if _, err := readDroppedImage(p); err != nil {
		t.Fatalf("file at cap should be accepted, got: %v", err)
	}
}

func TestReadDroppedImage_MissingFile(t *testing.T) {
	if _, err := readDroppedImage(filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// TestDispatchDroppedFiles_SkipsUnsupportedExtension verifies the extension
// filter (keyed off imagepolicy.ExtToMIME) rejects non-image extensions before
// any read happens. It only checks the policy lookup the dispatcher relies on,
// without needing a live Wails context.
func TestDispatchDroppedFiles_ExtensionPolicy(t *testing.T) {
	cases := map[string]bool{
		"photo.png":  true,
		"photo.PNG":  true, // dispatcher lowercases the ext
		"a.jpeg":     true,
		"a.webp":     true,
		"notes.txt":  false,
		"archive.zip": false,
		"noext":      false,
	}
	for name, want := range cases {
		_, ok := imagepolicy.ExtToMIME[strings.ToLower(filepath.Ext(name))]
		if ok != want {
			t.Errorf("ext policy for %q = %v, want %v", name, ok, want)
		}
	}
}
