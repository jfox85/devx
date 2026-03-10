//go:build windows

package cloudflare

import (
	"errors"
	"os"
	"path/filepath"
)

var errNotSupported = errors.New("cloudflared daemon not supported on Windows")

// DefaultPIDPath returns the default path for the cloudflared PID file.
func DefaultPIDPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".config", "devx", "cloudflared.pid")
}

func StartDaemon(cfgPath, tunnelID, pidPath string) (int, error) {
	return 0, errNotSupported
}

func StopDaemon(pidPath string) (int, error) {
	return 0, errNotSupported
}

func ReadPID(pidPath string) (int, error) {
	return 0, errNotSupported
}

func IsRunning(pid int) bool {
	return false
}

func ReloadDaemon(cfgPath, tunnelID, pidPath string) error {
	// Consistent with the Unix implementation: no-op when daemon is not running.
	return nil
}
