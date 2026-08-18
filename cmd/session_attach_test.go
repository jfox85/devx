package cmd

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/jfox85/devx/session"
)

func setupAttachTestStore(t *testing.T) (*session.SessionStore, *session.Session) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
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
	originalEnsure, originalAttach := ensureTmuxForAttach, attachReadyTmux
	t.Cleanup(func() { ensureTmuxForAttach, attachReadyTmux = originalEnsure, originalAttach })

	ensureCalled := false
	ensureTmuxForAttach = func(name string, got *session.Session) error {
		ensureCalled = true
		return nil
	}
	attachReadyTmux = func(name string, got *session.Session) error {
		if !ensureCalled {
			t.Fatal("attach ran before ensure")
		}
		reloaded, err := session.LoadSessions()
		if err != nil {
			t.Fatal(err)
		}
		if reloaded.Sessions[name].LastAttached.IsZero() {
			t.Fatal("attach handoff ran before activity was recorded")
		}
		return nil
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
			originalEnsure, originalAttach := ensureTmuxForAttach, attachReadyTmux
			t.Cleanup(func() { ensureTmuxForAttach, attachReadyTmux = originalEnsure, originalAttach })
			ensureTmuxForAttach = func(string, *session.Session) error { return nil }
			attachReadyTmux = func(string, *session.Session) error { return nil }
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

func TestReadyAttachDoesNotRecordActivityWhenEnsureFails(t *testing.T) {
	store, sess := setupAttachTestStore(t)
	originalEnsure, originalAttach := ensureTmuxForAttach, attachReadyTmux
	t.Cleanup(func() { ensureTmuxForAttach, attachReadyTmux = originalEnsure, originalAttach })

	ensureTmuxForAttach = func(string, *session.Session) error { return errors.New("runtime unavailable") }
	attachReadyTmux = func(string, *session.Session) error {
		t.Fatal("attach should not run")
		return nil
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
