package imagepolicy

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"testing"
)

// frontendPolicyPath resolves web/app/src/lib/imagePolicy.js relative to this
// test file, so it works regardless of the working directory.
func frontendPolicyPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = .../web/imagepolicy/frontend_drift_test.go
	return filepath.Join(filepath.Dir(thisFile), "..", "app", "src", "lib", "imagePolicy.js")
}

// parseJSArray extracts the string literals from `export const <name> = [...]`.
// The array body matcher uses [\s\S]*? so a multi-line / Prettier-reformatted
// array still parses (the . metacharacter would not cross newlines), and both
// single- and double-quoted literals are accepted — a harmless reformat must
// not turn into a false "could not find" CI failure that masquerades as real
// drift.
func parseJSArray(t *testing.T, src, name string) []string {
	t.Helper()
	re := regexp.MustCompile(`export const ` + regexp.QuoteMeta(name) + `\s*=\s*\[([\s\S]*?)\]`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("could not find `export const %s = [...]` in imagePolicy.js", name)
	}
	items := regexp.MustCompile(`'([^']*)'|"([^"]*)"`).FindAllStringSubmatch(m[1], -1)
	out := make([]string, 0, len(items))
	for _, it := range items {
		// Alternation: group 1 captures single-quoted literals, group 2 double-
		// quoted. Exactly one branch matches; the unused group is the empty string.
		// (An empty literal '' or "" yields "" from either branch, which is correct.)
		if it[1] != "" {
			out = append(out, it[1])
		} else {
			out = append(out, it[2])
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestFrontendPolicyInSync fails if the frontend mirror in imagePolicy.js drifts
// from the Go source of truth, keeping a policy change a single coordinated edit.
func TestFrontendPolicyInSync(t *testing.T) {
	raw, err := os.ReadFile(frontendPolicyPath(t))
	if err != nil {
		t.Fatalf("reading frontend imagePolicy.js: %v", err)
	}
	src := string(raw)

	gotTypes := parseJSArray(t, src, "ALLOWED_IMAGE_TYPES")
	wantTypes := sortedKeys(MIMEToExt)
	if !equalStrings(gotTypes, wantTypes) {
		t.Errorf("ALLOWED_IMAGE_TYPES drift:\n  frontend: %v\n  backend:  %v", gotTypes, wantTypes)
	}

	gotExts := parseJSArray(t, src, "ALLOWED_IMAGE_EXTS")
	wantExts := sortedKeys(ExtToMIME)
	if !equalStrings(gotExts, wantExts) {
		t.Errorf("ALLOWED_IMAGE_EXTS drift:\n  frontend: %v\n  backend:  %v", gotExts, wantExts)
	}
}

// TestPolicyMapsConsistent guards the two Go maps against each other: every
// MIME type must have an extension that maps back to that MIME type.
//
// This is intentionally one-directional (MIMEToExt -> ExtToMIME). The reverse
// is not total: ExtToMIME has alias extensions (e.g. ".jpeg") that share a MIME
// type whose canonical extension in MIMEToExt is different (".jpg"), so a strict
// reverse round-trip would false-positive on those aliases. Each ExtToMIME value
// must still be a known MIME type, which the check below verifies via membership.
func TestPolicyMapsConsistent(t *testing.T) {
	for mimeType, ext := range MIMEToExt {
		back, ok := ExtToMIME[ext]
		if !ok {
			t.Errorf("MIMEToExt[%q]=%q has no ExtToMIME entry", mimeType, ext)
			continue
		}
		if back != mimeType {
			t.Errorf("round-trip mismatch: %q -> %q -> %q", mimeType, ext, back)
		}
	}
	// Every extension alias must still resolve to a MIME type the server accepts.
	for ext, mimeType := range ExtToMIME {
		if _, ok := MIMEToExt[mimeType]; !ok {
			t.Errorf("ExtToMIME[%q]=%q is not an accepted MIME type", ext, mimeType)
		}
	}
}
