//go:build windows

package target

import (
	"fmt"
	"os"
)

func validateTrustedGatepostOwner(path string, _ os.FileInfo) error {
	// Windows exposes neither owner UIDs nor real permission bits, so trust
	// cannot be verified. Refuse explicitly rather than relying on Go's
	// synthesized directory mode tripping the writable-bits check.
	return fmt.Errorf("%s: %s", errGatepostTrustUnsupported, path)
}
