package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestCopyBootstrapFilesExplicitList(t *testing.T) {
	projectRoot := t.TempDir()
	worktree := t.TempDir()

	tokenPath := filepath.Join(projectRoot, ".devx-web-token")
	if err := os.WriteFile(tokenPath, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Explicit list must be honored regardless of global viper state.
	viper.Reset()
	if err := CopyBootstrapFiles(projectRoot, worktree, []string{".devx-web-token"}); err != nil {
		t.Fatalf("CopyBootstrapFiles: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(worktree, ".devx-web-token"))
	if err != nil {
		t.Fatalf("token not copied to worktree: %v", err)
	}
	if string(got) != "secret" {
		t.Fatalf("token content mismatch: %q", got)
	}
}

func TestCopyBootstrapFilesNilFallsBackToViper(t *testing.T) {
	projectRoot := t.TempDir()
	worktree := t.TempDir()

	if err := os.WriteFile(filepath.Join(projectRoot, "seed.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	viper.Reset()
	viper.Set("bootstrap_files", []string{"seed.txt"})
	t.Cleanup(viper.Reset)

	if err := CopyBootstrapFiles(projectRoot, worktree, nil); err != nil {
		t.Fatalf("CopyBootstrapFiles: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "seed.txt")); err != nil {
		t.Fatalf("viper fallback did not copy file: %v", err)
	}
}
