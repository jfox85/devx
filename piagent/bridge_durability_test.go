package piagent

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestBridgeDurabilityFaultInjection runs the bridge's fixture-only
// fault-injection suite (testdata/bridge-durability.test.mjs) under Node:
// fail-closed human takeover and lossless terminal receipts under lock
// timeouts, partial writes, restarts and unknown outcomes.
//
// Node 22.6+ is required to load the TypeScript bridge directly. Without it
// the test is skipped, unless DEVX_REQUIRE_BRIDGE_NODE_TESTS=1 (CI), which
// turns a missing or old Node into a failure.
func TestBridgeDurabilityFaultInjection(t *testing.T) {
	require := os.Getenv("DEVX_REQUIRE_BRIDGE_NODE_TESTS") == "1"
	skip := func(msg string) {
		if require {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		skip("node not installed")
	}
	out, err := exec.Command(node, "--version").Output()
	if err != nil {
		skip("node --version: " + err.Error())
	}
	m := regexp.MustCompile(`^v(\d+)\.(\d+)`).FindSubmatch(out)
	if m == nil {
		skip("unrecognized node version " + string(out))
	}
	major, _ := strconv.Atoi(string(m[1]))
	minor, _ := strconv.Atoi(string(m[2]))
	if major < 22 || (major == 22 && minor < 6) {
		skip("node " + string(out) + " cannot load TypeScript (need 22.6+)")
	}
	suite, err := filepath.Abs(filepath.Join("testdata", "bridge-durability.test.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, "--experimental-strip-types", "--no-warnings", "--test", "--test-reporter=tap", suite)
	cmd.Env = append(os.Environ(), "NODE_OPTIONS=")
	res, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bridge durability suite failed: %v\n%s", err, res)
	}
	if !regexp.MustCompile(`(?m)^# fail 0$`).Match(res) || !regexp.MustCompile(`(?m)^# pass [1-9]`).Match(res) {
		t.Fatalf("bridge durability suite did not report a clean pass:\n%s", res)
	}
	t.Logf("%s", regexp.MustCompile(`(?m)^# (pass|fail) \d+$`).FindAll(res, -1))
}
