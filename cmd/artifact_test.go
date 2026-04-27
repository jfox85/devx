package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	artifactpkg "github.com/jfox85/devx/artifact"
	"github.com/jfox85/devx/session"
	"github.com/spf13/cobra"
)

func setupArtifactCommandTest(t *testing.T) (*session.Session, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".config", "devx"), 0o755); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Name: "feature-artifacts", Branch: "feature-artifacts", Path: worktree, Ports: map[string]int{"ui": 3000}}
	store := &session.SessionStore{Sessions: map[string]*session.Session{sess.Name: sess}, NumberedSlots: map[int]string{}}
	if err := store.Save(); err != nil {
		t.Fatalf("Save sessions: %v", err)
	}
	return sess, home
}

func resetArtifactGlobals() {
	artifactSessionFlag = ""
	artifactAddFlags = struct {
		artifactType string
		title        string
		summary      string
		agent        string
		retention    string
		tags         string
		focus        bool
		id           string
		file         string
	}{}
	artifactListFlags = struct {
		artifactType string
		tag          string
		agent        string
		search       string
		json         bool
	}{}
	artifactURLFlags = struct {
		absolute bool
		local    bool
		embed    bool
	}{}
}

func TestArtifactAddListURL(t *testing.T) {
	defer resetArtifactGlobals()
	sess, _ := setupArtifactCommandTest(t)
	source := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(source, []byte("# Report"), 0o644); err != nil {
		t.Fatal(err)
	}

	artifactSessionFlag = sess.Name
	artifactAddFlags.title = "Completion Report"
	artifactAddFlags.artifactType = "report"
	artifactAddFlags.agent = "test-agent"
	artifactAddFlags.tags = "done, report"
	var addOut bytes.Buffer
	addCmd := &cobra.Command{}
	addCmd.SetOut(&addOut)
	if err := runArtifactAdd(addCmd, []string{source}); err != nil {
		t.Fatalf("runArtifactAdd: %v", err)
	}
	if got := strings.TrimSpace(addOut.String()); !strings.HasPrefix(got, "/sessions/feature-artifacts/artifacts/") {
		t.Fatalf("unexpected add output: %q", got)
	}

	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Artifacts) != 1 {
		t.Fatalf("expected one artifact, got %d", len(manifest.Artifacts))
	}
	added := manifest.Artifacts[0]
	if added.Type != "report" || added.Agent != "test-agent" || added.File != "report.md" {
		t.Fatalf("unexpected artifact: %#v", added)
	}
	if _, err := os.Stat(filepath.Join(sess.Path, ".artifacts", "report.md")); err != nil {
		t.Fatalf("artifact file missing: %v", err)
	}

	artifactListFlags.json = true
	var listOut bytes.Buffer
	listCmd := &cobra.Command{}
	listCmd.SetOut(&listOut)
	if err := runArtifactList(listCmd, nil); err != nil {
		t.Fatalf("runArtifactList: %v", err)
	}
	var listed []artifactpkg.ListItem
	if err := json.Unmarshal(listOut.Bytes(), &listed); err != nil {
		t.Fatalf("list JSON invalid: %v\n%s", err, listOut.String())
	}
	if len(listed) != 1 || listed[0].Path != ".artifacts/report.md" {
		t.Fatalf("unexpected list JSON: %#v", listed)
	}

	var urlOut bytes.Buffer
	urlCmd := &cobra.Command{}
	urlCmd.SetOut(&urlOut)
	if err := runArtifactURL(urlCmd, []string{added.ID}); err != nil {
		t.Fatalf("runArtifactURL: %v", err)
	}
	if got := strings.TrimSpace(urlOut.String()); got != "/sessions/feature-artifacts/artifacts/report.md" {
		t.Fatalf("unexpected URL: %q", got)
	}
}

func TestArtifactAddFromStdinRequiresFile(t *testing.T) {
	defer resetArtifactGlobals()
	sess, _ := setupArtifactCommandTest(t)
	artifactSessionFlag = sess.Name
	artifactAddFlags.title = "Stdin Report"
	artifactAddFlags.artifactType = "report"
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("hello"))
	if err := runArtifactAdd(cmd, []string{"-"}); err == nil {
		t.Fatal("expected --file required error")
	}
}

func TestArtifactAddFocusSetsAttentionFlag(t *testing.T) {
	defer resetArtifactGlobals()
	sess, _ := setupArtifactCommandTest(t)
	source := filepath.Join(t.TempDir(), "plan.html")
	if err := os.WriteFile(source, []byte("<h1>Plan</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifactSessionFlag = sess.Name
	artifactAddFlags.title = "Plan"
	artifactAddFlags.artifactType = "plan"
	artifactAddFlags.focus = true
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runArtifactAdd(cmd, []string{source}); err != nil {
		t.Fatalf("runArtifactAdd: %v", err)
	}
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	updated, ok := store.GetSession(sess.Name)
	if !ok || !updated.AttentionFlag || updated.AttentionReason != "New artifact: Plan" || updated.AttentionSource != "artifact" {
		t.Fatalf("attention flag not set: %#v", updated)
	}
	manifest, err := artifactpkg.LoadManifest(sess)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Artifacts) != 1 || !manifest.Artifacts[0].Focus {
		t.Fatalf("focus not set in manifest: %#v", manifest.Artifacts)
	}
}
