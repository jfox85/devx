package artifactbridge

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jfox85/devx/piagent"
)

// On platforms without openat/O_NOFOLLOW/linkat (Windows) the bridge must be
// fully disabled even with a permissive policy: no tools, no instructions,
// and every bridge call refused as unavailable. On supported platforms the
// same policy enables it. Runs everywhere.
func TestBridgeFailsClosedWithoutPlatformSupport(t *testing.T) {
	pol := Policy{Read: true, Upload: true, AllowedProjects: []string{"proj"}, SessionsSet: true}
	svc := New(Config{StateDir: filepath.Join(t.TempDir(), "bridge")}, piagent.NewStore(t.TempDir()), func() (Policy, error) { return pol, nil })
	tools, instr := svc.Tools(), svc.Instructions()
	res, handled := svc.CallTool(ToolList, json.RawMessage(`{"agent_id":"pa_aaaaaaaaaaaa"}`))
	if !handled {
		t.Fatal("bridge tool names must always be handled (refused, never passed through)")
	}
	if platformSupported {
		if len(tools) != 3 || instr == "" {
			t.Fatalf("supported platform: want 3 tools and instructions, got %d/%q", len(tools), instr)
		}
		return
	}
	if len(tools) != 0 || instr != "" {
		t.Fatalf("unsupported platform must expose no bridge tools: %d tools, instructions %q", len(tools), instr)
	}
	b, _ := json.Marshal(res)
	if res["isError"] != true || !json.Valid(b) || !strings.Contains(string(b), "unavailable") {
		t.Fatalf("unsupported platform must refuse as unavailable: %s", b)
	}
}
