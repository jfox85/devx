package install_test

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the actual installer against a local release fixture: installing a
// stable binary must link to that tag's docs even when main has newer commands.
func TestInstallerLinksDocumentationForResolvedRelease(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installer targets macOS and Linux")
	}
	for _, version := range []string{"latest", "0.3.0", "v0.3.0"} {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0755); err != nil {
				t.Fatal(err)
			}
			metadata := filepath.Join(dir, "release.json")
			// A long release body must be consumed fully: exiting grep early
			// makes curl fail with a broken pipe under the installer's pipefail.
			payload := "{\n  \"tag_name\": \"v0.3.0\",\n  \"body\": \"" + strings.Repeat("release notes ", 100000) + "\"\n}\n"
			if err := os.WriteFile(metadata, []byte(payload), 0600); err != nil {
				t.Fatal(err)
			}
			archive := filepath.Join(dir, "release.tar.gz")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(file)
			tw := tar.NewWriter(gz)
			content := []byte("#!/bin/sh\necho 'devx version 0.3.0'\n")
			if err := tw.WriteHeader(&tar.Header{Name: "devx", Mode: 0755, Size: int64(len(content))}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(content); err != nil {
				t.Fatal(err)
			}
			for _, close := range []func() error{tw.Close, gz.Close, file.Close} {
				if err := close(); err != nil {
					t.Fatal(err)
				}
			}
			curl := `#!/bin/sh
case "$2" in
 */releases/latest) cat "$FIXTURE_RELEASE_METADATA" ;;
 */releases/download/v0.3.0/devx_0.3.0_*.tar.gz) cp "$FIXTURE_ARCHIVE" "$4" ;;
 *) echo "unexpected URL: $2" >&2; exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(curl), 0755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "install.sh")
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "DEVX_VERSION="+version, "DEVX_INSTALL_DIR="+filepath.Join(dir, "installed"), "FIXTURE_ARCHIVE="+archive, "FIXTURE_RELEASE_METADATA="+metadata)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("installer: %v\n%s", err, output)
			}
			want := "Documentation for v0.3.0: https://github.com/jfox85/devx/blob/v0.3.0/README.md"
			if !strings.Contains(string(output), want) {
				t.Fatalf("missing version-matched guidance %q:\n%s", want, output)
			}
			if _, err := os.Stat(filepath.Join(dir, "installed", "devx")); err != nil {
				t.Fatal(fmt.Errorf("binary not installed: %w", err))
			}
		})
	}
}
