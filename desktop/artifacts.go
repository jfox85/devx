package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/session"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Artifact bindings. The WKWebView in the desktop shell cannot follow
// target=_blank links or perform downloads, so "open" and "download" for
// artifacts go through the host. The SPA only ever passes a session name and an
// artifact ID; the host resolves the file through the manifest
// (artifactpkg.ResolveFile), so the WebView can never name an arbitrary path.

func resolveArtifact(sessionName, id string) (string, *artifactpkg.Artifact, error) {
	store, err := session.LoadSessions()
	if err != nil {
		return "", nil, fmt.Errorf("load sessions: %w", err)
	}
	sess, ok := store.GetSession(sessionName)
	if !ok {
		return "", nil, fmt.Errorf("session %q not found", sessionName)
	}
	return artifactpkg.ResolveFile(sess, id)
}

// openCommand returns the command that opens abs with the OS default app, or —
// for file types that could execute code when opened — reveals it in the file
// manager instead.
func openCommand(goos, abs string) (*exec.Cmd, error) {
	safe := artifactpkg.SafeToOpenExternally(abs)
	switch goos {
	case "darwin":
		if safe {
			return exec.Command("open", "--", abs), nil
		}
		return exec.Command("open", "-R", "--", abs), nil
	case "linux":
		if safe {
			return exec.Command("xdg-open", abs), nil
		}
		return exec.Command("xdg-open", filepath.Dir(abs)), nil
	case "windows":
		// explorer.exe opens a file with its associated app, or with
		// /select, reveals it. It takes the path as a single argument with no
		// shell parsing (unlike `cmd /c start`), so a crafted file name cannot
		// inject commands.
		if safe {
			return exec.Command("explorer.exe", abs), nil
		}
		return exec.Command("explorer.exe", "/select,"+abs), nil
	default:
		return nil, fmt.Errorf("opening artifacts is not supported on %s", goos)
	}
}

// OpenArtifact opens an artifact with the user's default application.
func (h *Host) OpenArtifact(sessionName, id string) error {
	abs, _, err := resolveArtifact(sessionName, id)
	if err != nil {
		return err
	}
	cmd, err := openCommand(goruntime.GOOS, abs)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// DownloadArtifact shows a native Save dialog and copies the artifact to the
// chosen location. Returns the saved path, or "" if the user cancelled.
func (h *Host) DownloadArtifact(sessionName, id string) (string, error) {
	if h.ctx == nil {
		return "", fmt.Errorf("host not started")
	}
	abs, a, err := resolveArtifact(sessionName, id)
	if err != nil {
		return "", err
	}
	home, _ := os.UserHomeDir()
	defaultDir := ""
	if home != "" {
		defaultDir = filepath.Join(home, "Downloads")
	}
	dest, err := wailsruntime.SaveFileDialog(h.ctx, wailsruntime.SaveDialogOptions{
		Title:                "Save artifact",
		DefaultDirectory:     defaultDir,
		DefaultFilename:      filepath.Base(a.File),
		CanCreateDirectories: true,
	})
	if err != nil {
		return "", fmt.Errorf("save dialog: %w", err)
	}
	if dest == "" {
		return "", nil
	}
	if err := copyArtifactFile(abs, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// copyArtifactFile copies src to dest. The source is opened with O_NOFOLLOW so
// a symlink swapped in after ResolveFile's check is still refused. The data is
// written to a temp file beside dest and renamed into place, so a failed copy
// never leaves a truncated file where the user expects their download.
func copyArtifactFile(src, dest string) error {
	in, err := os.OpenFile(src, os.O_RDONLY|openNoFollow, 0)
	if err != nil {
		return fmt.Errorf("open artifact: %w", err)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat artifact: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("artifact is not a regular file")
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".devx-download-*")
	if err != nil {
		return fmt.Errorf("create download: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("write download: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write download: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("write download: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("save download: %w", err)
	}
	return nil
}
