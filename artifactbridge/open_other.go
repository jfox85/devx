//go:build !(darwin || linux)

package artifactbridge

import "os"

// platformSupported is false where openat/O_NOFOLLOW/linkat are unavailable
// (e.g. Windows). New disables both capabilities there: the bridge fails
// closed rather than falling back to check-then-open path handling.
const platformSupported = false

func fileIdentity(os.FileInfo) string { return "" }

func openUnderArtifacts(string, string) (*os.File, os.FileInfo, error) {
	return nil, nil, errUnavailable
}

func attachmentsDir(string) (int, error)              { return -1, errUnavailable }
func closeDir(int)                                    {}
func writeTemp(int, string, []byte) error             { return errUnavailable }
func linkNoReplace(int, string, string) (bool, error) { return false, errUnavailable }
func unlinkAt(int, string)                            {}
func shaAt(int, string) (string, error)               { return "", errUnavailable }
func readManifestNoFollow(string) ([]byte, error)     { return nil, errUnavailable }
