//go:build windows

package session

import (
	"os"
	"testing"
)

// Deterministic mechanism probe (validation only, never merged): does a
// writer's atomic rename onto sessions.json fail while another handle has
// the file open for reading, as the lock-free LoadSessions does?
func TestProbeRenameWhileReaderOpen(t *testing.T) {
	setupTempHome(t)
	st, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddSession("s1", "b", "/p", nil); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(getSessionsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	err = st.UpdateSession("s1", func(s *Session) { s.Branch = "x" })
	t.Logf("PROBE rename-with-open-reader err=%v", err)
	f.Close()
	err = st.UpdateSession("s1", func(s *Session) { s.Branch = "y" })
	t.Logf("PROBE rename-after-reader-closed err=%v", err)
}
