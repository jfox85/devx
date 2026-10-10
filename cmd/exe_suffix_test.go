package cmd

import (
	"runtime"
	"testing"
)

// exeSuffix is ".exe" on Windows: test-built binaries need it to be
// executable (exec resolves an extensionless path via PATHEXT there).
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// requireArtifactBridgePlatform skips binary end-to-end tests of a LIVE
// artifact bridge where it cannot run. Reason: the bridge needs
// openat/O_NOFOLLOW/linkat and is disabled by design on Windows (it fails
// closed; artifactbridge.TestBridgeFailsClosedWithoutPlatformSupport covers
// that on every platform).
func requireArtifactBridgePlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skipf("artifact bridge is disabled on %s (no openat/O_NOFOLLOW/linkat)", runtime.GOOS)
	}
}
