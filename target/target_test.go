package target

import (
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/jfox85/devx/session"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		input    string
		wantType string
		wantErr  bool
	}{
		{"", "host", false},
		{"host", "host", false},
		{"docker", "docker", false},
		{"vm", "", true},
		{"invalid", "", true},
	}
	for _, tt := range tests {
		tgt, err := Resolve(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("Resolve(%q) expected error, got nil", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("Resolve(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if tgt.Type() != tt.wantType {
			t.Errorf("Resolve(%q).Type() = %q, want %q", tt.input, tgt.Type(), tt.wantType)
		}
	}
}

func TestHostTargetStartStop(t *testing.T) {
	h := &HostTarget{}
	result, err := h.Start(context.TODO(), StartOpts{SessionName: "test"})
	if err != nil {
		t.Fatalf("HostTarget.Start: %v", err)
	}
	if result.Meta.Type != "host" {
		t.Errorf("Meta.Type = %q, want %q", result.Meta.Type, "host")
	}
	if err := h.Stop(context.TODO(), result.Meta); err != nil {
		t.Errorf("HostTarget.Stop: %v", err)
	}
}

func TestTargetMetaSerialization(t *testing.T) {
	meta := session.TargetMeta{
		Type:          "docker",
		ContainerID:   "abc123",
		ContainerName: "devx-test",
		NetworkName:   "devx-test-net",
		Image:         "ubuntu:24.04",
	}

	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded session.TargetMeta
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Type != meta.Type || decoded.ContainerID != meta.ContainerID || decoded.ContainerName != meta.ContainerName || decoded.NetworkName != meta.NetworkName || decoded.Image != meta.Image {
		t.Errorf("round-trip mismatch: got %+v, want %+v", decoded, meta)
	}
}

func TestTargetMetaBackwardCompat(t *testing.T) {
	// Empty JSON (existing session without target field) should unmarshal cleanly
	s := &session.Session{}
	data := []byte(`{"name":"test","branch":"main","path":"/tmp/test","ports":{"ui":3000},"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}`)
	if err := json.Unmarshal(data, s); err != nil {
		t.Fatalf("Unmarshal existing session: %v", err)
	}
	if s.TargetType() != "host" {
		t.Errorf("TargetType() = %q, want %q", s.TargetType(), "host")
	}
	if s.IsContainerized() {
		t.Error("IsContainerized() = true for host session")
	}
}

func TestTargetMetaDocker(t *testing.T) {
	s := &session.Session{
		Target: session.TargetMeta{Type: "docker", ContainerName: "devx-test"},
	}
	if s.TargetType() != "docker" {
		t.Errorf("TargetType() = %q, want %q", s.TargetType(), "docker")
	}
	if !s.IsContainerized() {
		t.Error("IsContainerized() = false for docker session")
	}
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		session string
		want    string
	}{
		{"my-session", "devx-my-session"},
		{"feature/foo", "devx-feature-foo"},
		{"test_thing", "devx-test-thing"},
		{"UPPER", "devx-upper"},
	}
	for _, tt := range tests {
		got := ContainerName(tt.session)
		if got != tt.want {
			t.Errorf("ContainerName(%q) = %q, want %q", tt.session, got, tt.want)
		}
	}
}

func TestNetworkName(t *testing.T) {
	got := NetworkName("my-session")
	want := "devx-my-session-net"
	if got != want {
		t.Errorf("NetworkName = %q, want %q", got, want)
	}
}

func TestExecInSessionHost(t *testing.T) {
	cmd := ExecInSession(session.TargetMeta{}, []string{"echo", "hello"}, false)
	if cmd.Path == "" {
		t.Fatal("ExecInSession returned nil cmd")
	}
	// For host, it should be "echo" not "docker"
	if len(cmd.Args) < 1 || cmd.Args[0] != "echo" {
		t.Errorf("host exec: args = %v, want [echo hello]", cmd.Args)
	}
}

func TestExecInSessionDocker(t *testing.T) {
	meta := session.TargetMeta{Type: "docker", ContainerName: "devx-test"}

	cmd := ExecInSession(meta, []string{"ls", "/workspace"}, false)
	// Should be: docker exec devx-test ls /workspace
	want := []string{"docker", "exec", "devx-test", "ls", "/workspace"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("docker exec: args = %v, want %v", cmd.Args, want)
	}
	for i, a := range cmd.Args {
		if a != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, a, want[i])
		}
	}
}

func TestExecInSessionDockerInteractive(t *testing.T) {
	meta := session.TargetMeta{Type: "docker", ContainerName: "devx-test"}

	cmd := ExecInSession(meta, []string{"/bin/bash"}, true)
	want := []string{"docker", "exec", "-it", "devx-test", "/bin/bash"}
	if len(cmd.Args) != len(want) {
		t.Fatalf("docker exec -it: args = %v, want %v", cmd.Args, want)
	}
	for i, a := range cmd.Args {
		if a != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, a, want[i])
		}
	}
}

func TestDefaultSecurityOpts(t *testing.T) {
	opts := DefaultSecurityOpts()
	if opts.MemoryLimit != "4g" {
		t.Errorf("MemoryLimit = %q, want %q", opts.MemoryLimit, "4g")
	}
	if opts.CPULimit != "4" {
		t.Errorf("CPULimit = %q, want %q", opts.CPULimit, "4")
	}
	if opts.PidsLimit != 2048 {
		t.Errorf("PidsLimit = %d, want %d", opts.PidsLimit, 2048)
	}
	if len(opts.CapDrop) != 1 || opts.CapDrop[0] != "ALL" {
		t.Errorf("CapDrop = %v, want [ALL]", opts.CapDrop)
	}
	if !opts.NoNewPrivs {
		t.Error("NoNewPrivs = false, want true")
	}
}

func TestCheckAvailable(t *testing.T) {
	// Only run if docker is actually present
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not in PATH")
	}
	// We just check it doesn't panic; whether it passes depends on Docker running
	_ = CheckAvailable()
}
