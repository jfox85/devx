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
	for _, version := range []string{"", "latest", "0.3.0", "v0.3.0"} {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.Mkdir(bin, 0755); err != nil {
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
 https://api.github.com/*) echo 'API rate limit exceeded' >&2; exit 22 ;;
 https://github.com/jfox85/devx/releases/latest)
   [ "$3" = "-o" ] && [ "$4" = "/dev/null" ] && [ "$5" = "-w" ] && [ "$6" = '%{url_effective}' ] || exit 99
   printf '%s' 'https://github.com/jfox85/devx/releases/tag/v0.3.0' ;;
 */releases/download/v0.3.0/devx_0.3.0_*.tar.gz) cp "$FIXTURE_ARCHIVE" "$4" ;;
 *) echo "unexpected URL: $2" >&2; exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(curl), 0755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "install.sh")
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "DEVX_VERSION="+version, "DEVX_INSTALL_DIR="+filepath.Join(dir, "installed"), "FIXTURE_ARCHIVE="+archive)
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

// Discovery errors must stop before downloading or replacing a user's binary.
func TestInstallerRejectsFailedLatestDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("installer targets macOS and Linux")
	}
	for _, tc := range []struct{ name, url, status string }{
		{"network failure", "", "22"},
		{"no release", "https://github.com/jfox85/devx/releases/latest", "0"},
		{"different repository", "https://github.com/other/devx/releases/tag/v0.4.0", "0"},
		{"invalid tag", "https://github.com/jfox85/devx/releases/tag/v0.4.0/other", "0"},
		{"empty tag", "https://github.com/jfox85/devx/releases/tag/", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			installed := filepath.Join(dir, "devx")
			original := []byte("existing binary")
			if err := os.WriteFile(installed, original, 0755); err != nil {
				t.Fatal(err)
			}
			curl := `#!/bin/sh
if [ "$2" != "https://github.com/jfox85/devx/releases/latest" ]; then
 echo 'unexpected download' >&2
 exit 99
fi
printf '%s' "$FIXTURE_URL"
exit "$FIXTURE_STATUS"
`
			if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(curl), 0755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "install.sh")
			cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "DEVX_VERSION=latest", "DEVX_INSTALL_DIR="+dir, "FIXTURE_URL="+tc.url, "FIXTURE_STATUS="+tc.status)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "Unable to determine latest release version") || strings.Contains(string(output), "unexpected download") {
				t.Fatalf("expected discovery failure, got %v:\n%s", err, output)
			}
			got, err := os.ReadFile(installed)
			if err != nil || string(got) != string(original) {
				t.Fatalf("existing binary changed: %q, %v", got, err)
			}
		})
	}
}
