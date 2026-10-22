package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestConfigInitCreatesMissingFilesAndPreservesExisting(t *testing.T) {
	for _, existing := range []string{"", "config", "template", "both"} {
		t.Run(existing, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			original := cfgFile
			cfgFile = ""
			t.Cleanup(func() { cfgFile = original })
			dir := filepath.Join(home, ".config", "devx")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			paths := map[string]string{"config": filepath.Join(dir, "config.yaml"), "template": filepath.Join(dir, "session.yaml.tmpl")}
			for kind, path := range paths {
				if existing == kind || existing == "both" {
					if err := os.WriteFile(path, []byte("# user-owned "+kind+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			cmd := &cobra.Command{}
			var output bytes.Buffer
			cmd.SetOut(&output)
			for i := 0; i < 2; i++ {
				if err := runConfigInit(cmd, nil); err != nil {
					t.Fatal(err)
				}
			}
			for kind, path := range paths {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if existing == kind || existing == "both" {
					if string(data) != "# user-owned "+kind+"\n" {
						t.Fatalf("overwrote %s", kind)
					}
				} else if kind == "config" {
					v := viper.New()
					v.SetConfigFile(path)
					if err := v.ReadInConfig(); err != nil {
						t.Fatal(err)
					}
					if v.GetString("basedomain") != "localhost" || len(v.GetStringSlice("ports")) != 2 {
						t.Fatalf("invalid starter config: %s", data)
					}
					if !strings.Contains(string(data), "tmuxp_template:") {
						t.Fatal("missing portable template setting")
					}
				}
			}
			if strings.Contains(output.String(), "--force") {
				t.Fatal("advertised nonexistent overwrite flag")
			}
		})
	}
}

func TestConfigInitHonorsExplicitConfigWithoutCopyingEffectiveSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	original := cfgFile
	cfgFile = filepath.Join(t.TempDir(), "nested", "custom.yaml")
	t.Cleanup(func() { cfgFile = original })
	oldSecret := viper.Get("web_secret_token")
	viper.Set("web_secret_token", "must-not-copy")
	t.Cleanup(func() { viper.Set("web_secret_token", oldSecret) })
	if err := runConfigInit(&cobra.Command{}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "must-not-copy") {
		t.Fatal("leaked effective settings into starter config")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "devx", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("unexpected global config: %v", err)
	}
}

func TestConfigInitReportsInvalidDestination(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	original := cfgFile
	cfgFile = filepath.Join(parent, "config.yaml")
	t.Cleanup(func() { cfgFile = original })
	if err := runConfigInit(&cobra.Command{}, nil); err == nil {
		t.Fatal("expected destination error")
	}
}

func TestConfigInitRejectsDirectoryAtConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	original := cfgFile
	cfgFile = t.TempDir()
	t.Cleanup(func() { cfgFile = original })
	if err := runConfigInit(&cobra.Command{}, nil); err == nil {
		t.Fatal("directory reported as initialized config file")
	}
}
