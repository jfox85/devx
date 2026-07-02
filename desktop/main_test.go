//go:build darwin || linux

package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

// TestDropEventNames pins the Go side of the DOM event-name contract; the JS
// mirror in web/app/src/lib/desktopBridge.js (DESKTOP_EVENTS) is checked against
// these same constants by TestDesktopBridgeEventNamesInSync.
func TestDropEventNames(t *testing.T) {
	if eventFileDrop != "devx:desktop:filedrop" {
		t.Errorf("eventFileDrop = %q, want devx:desktop:filedrop", eventFileDrop)
	}
	if eventFileDropRejected != "devx:desktop:filedrop-rejected" {
		t.Errorf("eventFileDropRejected = %q, want devx:desktop:filedrop-rejected", eventFileDropRejected)
	}
}

// TestDesktopBridgeEventNamesInSync closes the cross-language drift gap that
// TestDropEventNames alone cannot: it parses the JS mirror in
// web/app/src/lib/desktopBridge.js (DESKTOP_EVENTS) and asserts each event-name
// literal matches the Go constant, so a rename on either side fails CI.
func TestDesktopBridgeEventNamesInSync(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = .../desktop/main_test.go
	jsPath := filepath.Join(filepath.Dir(thisFile), "..", "web", "app", "src", "lib", "desktopBridge.js")
	raw, err := os.ReadFile(jsPath)
	if err != nil {
		t.Fatalf("reading desktopBridge.js: %v", err)
	}
	src := string(raw)

	// Match `fileDrop: '...'` / `fileDropRejected: '...'` with single or double
	// quotes, tolerant of reformatting.
	find := func(key string) string {
		re := regexp.MustCompile(key + `\s*:\s*'([^']*)'|` + key + `\s*:\s*"([^"]*)"`)
		m := re.FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("could not find %s in DESKTOP_EVENTS", key)
		}
		if m[1] != "" {
			return m[1]
		}
		return m[2]
	}

	if got := find("fileDrop"); got != eventFileDrop {
		t.Errorf("DESKTOP_EVENTS.fileDrop = %q, Go eventFileDrop = %q", got, eventFileDrop)
	}
	if got := find("fileDropRejected"); got != eventFileDropRejected {
		t.Errorf("DESKTOP_EVENTS.fileDropRejected = %q, Go eventFileDropRejected = %q", got, eventFileDropRejected)
	}
}

// TestBuildDropEvents_RejectsUnreadableAllowedExtension covers the branch where
// the extension passes the policy (.png) but readDroppedImage fails (here a
// symlinked leaf): the file must land in rejected, not accepted, and not abort
// the rest of the drop.
func TestBuildDropEvents_RejectsUnreadableAllowedExtension(t *testing.T) {
	dir := t.TempDir()
	target := writeTemp(t, dir, "secret", 16)
	link := filepath.Join(dir, "evil.png") // allowed extension, unreadable via O_NOFOLLOW
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	good := writeTemp(t, dir, "ok.png", 8)

	accepted, rejected := buildDropEvents([]string{link, good})

	if len(accepted) != 1 || accepted[0].Name != "ok.png" {
		t.Fatalf("accepted = %v, want only ok.png", accepted)
	}
	if len(rejected) != 1 || rejected[0] != "evil.png" {
		t.Errorf("rejected = %v, want [evil.png]", rejected)
	}
}

// TestBuildDropEvents_PayloadAndPartition asserts the accepted-payload shape
// (name/type/base64 data the SPA's fileFromBase64 decodes) and that a mixed
// drop partitions into accepted + rejected correctly.
func TestBuildDropEvents_PayloadAndPartition(t *testing.T) {
	dir := t.TempDir()
	png := writeTemp(t, dir, "shot.png", 8)
	txt := writeTemp(t, dir, "notes.txt", 8)

	accepted, rejected := buildDropEvents([]string{png, txt})

	if len(accepted) != 1 {
		t.Fatalf("accepted = %d payloads, want 1", len(accepted))
	}
	got := accepted[0]
	if got.Name != "shot.png" {
		t.Errorf("Name = %q, want shot.png", got.Name)
	}
	if got.Type != "image/png" {
		t.Errorf("Type = %q, want image/png", got.Type)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Data)
	if err != nil {
		t.Fatalf("Data is not valid base64: %v", err)
	}
	if len(decoded) != 8 {
		t.Errorf("decoded payload = %d bytes, want 8", len(decoded))
	}

	if len(rejected) != 1 || rejected[0] != "notes.txt" {
		t.Errorf("rejected = %v, want [notes.txt]", rejected)
	}

	// The accepted slice must marshal to the JSON array the SPA expects.
	encoded, err := json.Marshal(accepted)
	if err != nil {
		t.Fatalf("marshal accepted: %v", err)
	}
	var roundtrip []map[string]string
	if err := json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatalf("accepted payload is not a JSON object array: %v", err)
	}
	for _, key := range []string{"name", "type", "data"} {
		if _, ok := roundtrip[0][key]; !ok {
			t.Errorf("payload JSON missing %q key", key)
		}
	}
}

// TestBuildDropEvents_EmptyAndAllRejected covers the nil-slice contract used by
// the len()>0 dispatch guards.
func TestBuildDropEvents_EmptyAndAllRejected(t *testing.T) {
	if accepted, rejected := buildDropEvents(nil); accepted != nil || rejected != nil {
		t.Errorf("empty input: accepted=%v rejected=%v, want nil/nil", accepted, rejected)
	}

	dir := t.TempDir()
	txt := writeTemp(t, dir, "a.txt", 4)
	accepted, rejected := buildDropEvents([]string{txt})
	if accepted != nil {
		t.Errorf("accepted = %v, want nil", accepted)
	}
	if len(rejected) != 1 || rejected[0] != "a.txt" {
		t.Errorf("rejected = %v, want [a.txt]", rejected)
	}
}

// TestDispatchDroppedFiles_ExtensionPolicy verifies the extension filter
// (keyed off imagepolicy.ExtToMIME) rejects non-image extensions before any
// read happens. It only checks the policy lookup the dispatcher relies on,
// without needing a live Wails context.
func TestDispatchDroppedFiles_ExtensionPolicy(t *testing.T) {
	cases := map[string]bool{
		"photo.png":   true,
		"photo.PNG":   true, // dispatcher lowercases the ext
		"a.jpeg":      true,
		"a.webp":      true,
		"notes.txt":   false,
		"archive.zip": false,
		"noext":       false,
	}
	for name, want := range cases {
		_, ok := imagepolicy.ExtToMIME[strings.ToLower(filepath.Ext(name))]
		if ok != want {
			t.Errorf("ext policy for %q = %v, want %v", name, ok, want)
		}
	}
}

// TestUploadImageRejectsOversize verifies the size guard rejects an oversize
// payload (via the pre-decode and post-decode checks) before any HTTP call, so
// a nil server is never reached. An exactly-at-cap payload must NOT be rejected
// by the cheap base64 pre-check (padding rounding); that boundary is exercised
// by the integration upload path, not here, to avoid hitting the live server.
func TestUploadImageRejectsOversize(t *testing.T) {
	h := &Host{}
	over := base64.StdEncoding.EncodeToString(make([]byte, maxDroppedFileBytes+1))
	_, err := h.UploadImage("x.png", "", over)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversize cap error, got %v", err)
	}
}
