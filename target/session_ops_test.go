package target

import (
	"testing"

	"github.com/jfox85/devx/session"
)

func TestTargetSessionOperationsImplemented(t *testing.T) {
	for _, typ := range []string{"", "host", "docker", "gatepost"} {
		tgt, err := Resolve(typ)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", typ, err)
		}
		if tgt == nil {
			t.Fatalf("Resolve(%q) returned nil target", typ)
		}
		if err := tgt.KillTmuxServer(session.TargetMeta{Type: typ}); typ == "" || typ == "host" || typ == "gatepost" {
			if err != nil {
				t.Fatalf("KillTmuxServer(%q) should be a no-op, got %v", typ, err)
			}
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
