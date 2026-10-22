package cmd

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfox85/devx/session"
)

func setupAttachTestStore(t *testing.T) (*session.SessionStore, *session.Session) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddSession("demo", "main", t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	return store, store.Sessions["demo"]
}

func TestReadyAttachRecordsActivityAfterEnsureAndBeforeHandoff(t *testing.T) {
	store, sess := setupAttachTestStore(t)
	originalEnsure, originalAttach := ensureTmuxForAttach, startReadyTmux
	t.Cleanup(func() { ensureTmuxForAttach, startReadyTmux = originalEnsure, originalAttach })

	ensureCalled := false
	ensureTmuxForAttach = func(name string, got *session.Session) error {
		ensureCalled = true
		return nil
	}
	startReadyTmux = func(name string, got *session.Session) (func() error, error) {
		if !ensureCalled {
			t.Fatal("attach started before ensure")
		}
		return func() error {
			reloaded, err := session.LoadSessions()
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Sessions[name].LastAttached.IsZero() {
				t.Fatal("attach wait ran before activity was recorded")
			}
			return nil
		}, nil
	}

	if err := readyAttach(store, "demo", sess); err != nil {
		t.Fatal(err)
	}
}

func TestCreatedSessionHandoffRecordsActivityForHostAndContainer(t *testing.T) {
	for _, targetType := range []string{"host", "docker"} {
		t.Run(targetType, func(t *testing.T) {
			store, sess := setupAttachTestStore(t)
			sess.Target.Type = targetType
			if err := store.UpdateSession("demo", func(stored *session.Session) { stored.Target.Type = targetType }); err != nil {
				t.Fatal(err)
			}
			originalEnsure, originalAttach := ensureTmuxForAttach, startReadyTmux
			t.Cleanup(func() { ensureTmuxForAttach, startReadyTmux = originalEnsure, originalAttach })
			ensureTmuxForAttach = func(string, *session.Session) error { return nil }
			startReadyTmux = func(string, *session.Session) (func() error, error) {
				return func() error { return nil }, nil
			}
			t.Setenv("TMUX", "")

			launchCreatedSessionTmux("demo", sess)

			reloaded, err := session.LoadSessions()
			if err != nil {
				t.Fatal(err)
			}
			if reloaded.Sessions["demo"].LastAttached.IsZero() {
				t.Fatalf("%s creation handoff did not record activity", targetType)
			}
		})
	}
}

func TestReadyAttachDoesNotRecordActivityWhenHandoffFailsToStart(t *testing.T) {
	store, sess := setupAttachTestStore(t)
	originalEnsure, originalAttach := ensureTmuxForAttach, startReadyTmux
	t.Cleanup(func() { ensureTmuxForAttach, startReadyTmux = originalEnsure, originalAttach })
	ensureTmuxForAttach = func(string, *session.Session) error { return nil }
	startReadyTmux = func(string, *session.Session) (func() error, error) { return nil, errors.New("attach start failed") }

	if err := readyAttach(store, "demo", sess); err == nil {
		t.Fatal("expected attach failure")
	}
	reloaded, err := session.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Sessions["demo"].LastAttached.IsZero() {
		t.Fatal("failed handoff recorded activity")
	}
}

func TestReadyAttachDoesNotRecordActivityWhenEnsureFails(t *testing.T) {
	store, sess := setupAttachTestStore(t)
	originalEnsure, originalAttach := ensureTmuxForAttach, startReadyTmux
	t.Cleanup(func() { ensureTmuxForAttach, startReadyTmux = originalEnsure, originalAttach })

	ensureTmuxForAttach = func(string, *session.Session) error { return errors.New("runtime unavailable") }
	startReadyTmux = func(string, *session.Session) (func() error, error) {
		t.Fatal("attach should not run")
		return nil, nil
	}

	if err := readyAttach(store, "demo", sess); err == nil {
		t.Fatal("expected ensure failure")
	}
	reloaded, err := session.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Sessions["demo"].LastAttached.IsZero() {
		t.Fatal("failed ensure recorded activity")
	}
}

func TestFailedCreationHandoffExplainsWorktreeOnlyRecovery(t *testing.T) {
	_, sess := setupAttachTestStore(t)
	original := ensureTmuxForAttach
	t.Cleanup(func() { ensureTmuxForAttach = original })
	ensureTmuxForAttach = func(string, *session.Session) error { return errors.New("tmuxp not found in PATH") }
	t.Setenv("TMUX", "")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = writer
	launchCreatedSessionTmux("demo", sess)
	os.Stdout = originalStdout
	_ = writer.Close()
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	for _, want := range []string{"tmuxp not found", "devx check", "devx session attach demo", sess.Path, "--no-tmux"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in recovery output: %s", want, output)
		}
	}
	store, err := session.LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := store.GetSession("demo"); !exists {
		t.Fatal("failed terminal launch removed usable worktree metadata")
	}
}
