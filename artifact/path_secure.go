package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func EnsureArtifactDir(base string) error {
	if info, err := os.Lstat(base); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact directory is a symlink")
		}
		if !info.IsDir() {
			return fmt.Errorf("artifact path is not a directory")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(base)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("artifact directory is a symlink")
	}
	return nil
}

// SecureExistingPath resolves rel under base and rejects symlinks in any path
// component. It is intended for serving/copying/removing existing artifact files
// without letting a symlink inside .artifacts escape the artifact directory.
func SecureExistingPath(base, rel string) (string, error) {
	abs, err := SafeJoin(base, rel)
	if err != nil {
		return "", err
	}
	if err := rejectSymlinkComponents(base, rel, true); err != nil {
		return "", err
	}
	return abs, nil
}

// SecureNewPath resolves rel under base and rejects symlinks in parent
// components. If the destination exists and is a symlink, it is rejected too.
func SecureNewPath(base, rel string) (string, error) {
	abs, err := SafeJoin(base, rel)
	if err != nil {
		return "", err
	}
	if err := rejectSymlinkComponents(base, rel, false); err != nil {
		return "", err
	}
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("artifact path contains symlink")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return abs, nil
}

func rejectSymlinkComponents(base, rel string, requireLeaf bool) error {
	if err := ValidateRelativePath(rel); err != nil {
		return err
	}
	if err := EnsureArtifactDir(base); err != nil {
		return err
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	current := baseAbs
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if requireLeaf {
				return err
			}
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact path contains symlink")
		}
	}
	return nil
}
