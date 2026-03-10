//go:build windows

package cloudflare

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultPIDPath returns the default path for the cloudflared PID file.
func DefaultPIDPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".config", "devx", "cloudflared.pid")
}

func StartDaemon(cfgPath, tunnelID, pidPath string) (int, error) {
	return 0, fmt.Errorf("cloudflared daemon not supported on Windows")
}

func StopDaemon(pidPath string) (int, error) {
	return 0, fmt.Errorf("cloudflared daemon not supported on Windows")
}

func ReadPID(pidPath string) (int, error) {
	return 0, fmt.Errorf("cloudflared daemon not supported on Windows")
}

func IsRunning(pid int) bool {
	return false
}

func ReloadDaemon(cfgPath, tunnelID, pidPath string) error {
	return fmt.Errorf("cloudflared daemon not supported on Windows")
}
