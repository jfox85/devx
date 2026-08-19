package cloudflare

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home dir: %v", err)
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"tilde slash expands to home", "~/foo/bar", filepath.Join(home, "foo/bar")},
		{"tilde slash root", "~/", home},
		{"absolute path unchanged", "/etc/hosts", "/etc/hosts"},
		{"relative path unchanged", "foo/bar", "foo/bar"},
		{"empty unchanged", "", ""},
		// A bare "~" or "~user" form (no leading "~/") is not expanded.
		{"bare tilde unchanged", "~", "~"},
		{"tilde without slash unchanged", "~config", "~config"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandPath(tc.in); got != tc.want {
				t.Errorf("expandPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
