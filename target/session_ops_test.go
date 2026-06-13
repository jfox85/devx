package target

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jfox85/devx/session"
)

func TestResolveSessionOperator(t *testing.T) {
	tests := []struct {
		typ      string
		wantType string
	}{
		{"", "target.hostSessionOperator"},
		{"host", "target.hostSessionOperator"},
		{"docker", "target.dockerSessionOperator"},
		{"gatepost", "target.gatepostSessionOperator"},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			op, err := ResolveSessionOperator(session.TargetMeta{Type: tt.typ})
			if err != nil {
				t.Fatalf("ResolveSessionOperator(%q): %v", tt.typ, err)
			}
			if got := reflect.TypeOf(op).String(); got != tt.wantType {
				t.Fatalf("operator type = %s, want %s", got, tt.wantType)
			}
		})
	}
}

func TestSessionOperationDispatchErrors(t *testing.T) {
	if err := EnsureTmuxSession("demo", &session.Session{Target: session.TargetMeta{Type: "vm"}}); err == nil {
		t.Fatal("unknown target EnsureTmuxSession should fail")
	}
	if err := AttachTmuxSession("demo", nil); err == nil {
		t.Fatal("nil session AttachTmuxSession should fail")
	}
	if err := EnsureTmuxSession("demo", &session.Session{Target: session.TargetMeta{Type: "docker"}}); err == nil || !strings.Contains(err.Error(), "no runtime container") {
		t.Fatalf("docker without container should fail with runtime-container error, got %v", err)
	}
	if err := EnsureTmuxSession("demo", &session.Session{Target: session.TargetMeta{Type: "gatepost"}}); err == nil || !strings.Contains(err.Error(), "no runtime container") {
		t.Fatalf("gatepost without container should fail with runtime-container error, got %v", err)
	}
}

func TestNoopKillTmuxServerTargets(t *testing.T) {
	for _, typ := range []string{"", "host", "gatepost"} {
		if err := KillTmuxServer(session.TargetMeta{Type: typ}); err != nil {
			t.Fatalf("KillTmuxServer(%q) should be no-op, got %v", typ, err)
		}
	}
}

func TestIsRunningUnknownTargetFalse(t *testing.T) {
	if IsRunning(session.TargetMeta{Type: "vm"}) {
		t.Fatal("unknown target should not report running")
	}
}

func TestRuntimeName(t *testing.T) {
	if got := RuntimeName(session.TargetMeta{Type: "docker", ContainerName: "devx-demo"}); got != "devx-demo" {
		t.Fatalf("RuntimeName with container = %q, want devx-demo", got)
	}
	if got := RuntimeName(session.TargetMeta{Type: "host"}); got != "host" {
		t.Fatalf("RuntimeName without container = %q, want host", got)
	}
}
