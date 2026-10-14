//go:build !(darwin || linux)

package artifactbridge

import "os"

func openPolicy(path string) (*os.File, error) { return os.Open(path) }

// The bridge is disabled on these platforms (platformSupported=false).
func policyFileTrusted(os.FileInfo) error { return nil }
