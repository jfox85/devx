package update

import (
	"testing"
)

func TestDetectInstallMethod(t *testing.T) {
	// Note: This test is limited as it depends on the actual installation
	// In a real test environment, we'd mock os.Executable and os.Readlink
	method := DetectInstallMethod()

	// Should return a valid method
	validMethods := map[InstallMethod]bool{
		InstallMethodHomebrew:  true,
		InstallMethodGoInstall: true,
		InstallMethodManual:    true,
		InstallMethodUnknown:   true,
	}

	if !validMethods[method] {
		t.Errorf("DetectInstallMethod returned invalid method: %v", method)
	}
}

func TestCanSelfUpdate(t *testing.T) {
	// This test depends on the installation method
	// Just verify it returns a boolean
	result := CanSelfUpdate()

	// Should be a boolean (no panic)
	if result {
		t.Log("Self-update is supported")
	} else {
		t.Log("Self-update is not supported")
	}
}

func TestGetUpdateInstructions(t *testing.T) {
	instructions := GetUpdateInstructions()

	if instructions == "" {
		t.Error("GetUpdateInstructions returned empty string")
	}

	// Should contain some helpful text
	if len(instructions) < 10 {
		t.Errorf("GetUpdateInstructions returned too short message: %s", instructions)
	}
}
