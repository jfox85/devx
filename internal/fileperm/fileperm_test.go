package fileperm

import (
	"os"
	"testing"
)

func TestAccessibleByOthers(t *testing.T) {
	cases := []struct {
		mode, mask os.FileMode
		want       bool
	}{
		{0o600, 0o077, false},
		{0o644, 0o077, true},
		{0o755, 0o022, false},
		{0o775, 0o022, true},
	}
	for _, tc := range cases {
		got := AccessibleByOthers(tc.mode, tc.mask)
		want := tc.want && ModeBitsEnforced
		if got != want {
			t.Errorf("AccessibleByOthers(%04o, %04o) = %v, want %v (ModeBitsEnforced=%v)", tc.mode, tc.mask, got, want, ModeBitsEnforced)
		}
	}
}
