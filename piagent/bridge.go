package piagent

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed bridge/devx-bridge.ts
var bridgeSource []byte

// BridgeSource returns the embedded Pi bridge extension.
func BridgeSource() []byte { return append([]byte(nil), bridgeSource...) }

// WriteBridge installs the bridge extension under root (not in Pi's global
// extension directory, so it never loads into unrelated Pi sessions) and
// returns its path. It is rewritten only when the content changed.
func WriteBridge(root string) (string, error) {
	path := filepath.Join(root, "bridge", "devx-bridge.ts")
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, bridgeSource) {
		return path, nil
	}
	if err := writeFileAtomic(path, bridgeSource); err != nil {
		return "", err
	}
	return path, nil
}
