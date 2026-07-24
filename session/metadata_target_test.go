package session

import (
	"path/filepath"
	"testing"
)

func TestSessionTargetType(t *testing.T) {
	cases := []struct {
		name       string
		targetType string
		want       string
	}{
		{"empty defaults to host", "", "host"},
		{"explicit host", "host", "host"},
		{"docker", "docker", "docker"},
		{"gatepost", "gatepost", "gatepost"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{Target: TargetMeta{Type: tc.targetType}}
			if got := s.TargetType(); got != tc.want {
				t.Errorf("TargetType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionIsContainerized(t *testing.T) {
	cases := []struct {
		name       string
		targetType string
		want       bool
	}{
		{"empty is not containerized", "", false},
		{"host is not containerized", "host", false},
		{"docker is containerized", "docker", true},
		{"gatepost is containerized", "gatepost", true},
		// Any non-host type is treated as containerized (source uses != "host").
		{"unknown type is containerized", "podman", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Session{Target: TargetMeta{Type: tc.targetType}}
			if got := s.IsContainerized(); got != tc.want {
				t.Errorf("IsContainerized() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSessionsMetadataFingerprint(t *testing.T) {
	tmpDir := t.TempDir()

	// t.Setenv restores HOME automatically and marks the test as incompatible
	// with t.Parallel(), preventing a future parallel test from racing on the
	// shared HOME while the sessions metadata path is redirected here.
	t.Setenv("HOME", tmpDir)

	// Before any sessions file exists, the fingerprint reports "missing".
	if fp := SessionsMetadataFingerprint(); fp != "missing" {
		t.Errorf("expected 'missing' fingerprint before file exists, got %q", fp)
	}

	// Create a sessions store so the metadata file is written to disk.
	store, err := LoadSessions()
	if err != nil {
		t.Fatalf("failed to load sessions: %v", err)
	}
	if err := store.AddSession("s1", "main", filepath.Join(tmpDir, "wt"), map[string]int{"ui": 3000}); err != nil {
		t.Fatalf("failed to add session: %v", err)
	}

	fp1 := SessionsMetadataFingerprint()
	if fp1 == "missing" || fp1 == "" {
		t.Fatalf("expected a real fingerprint after writing metadata, got %q", fp1)
	}

	// A stable file should yield a stable fingerprint.
	if fp2 := SessionsMetadataFingerprint(); fp2 != fp1 {
		t.Errorf("fingerprint changed without a write: %q -> %q", fp1, fp2)
	}

	// Mutating the store should change size and/or modtime, changing the
	// fingerprint so watchers can detect the update.
	if err := store.AddSession("s2", "main", filepath.Join(tmpDir, "wt2"), map[string]int{"api": 4000}); err != nil {
		t.Fatalf("failed to add second session: %v", err)
	}
	if fp3 := SessionsMetadataFingerprint(); fp3 == fp1 {
		t.Errorf("expected fingerprint to change after metadata write, still %q", fp3)
	}
}
