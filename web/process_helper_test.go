package web

import (
	"os"
	"testing"
	"time"
)

func testSleepCommand(t *testing.T, duration time.Duration) []string {
	t.Helper()
	t.Setenv("GO_WANT_WEB_HELPER_PROCESS", "1")
	return []string{os.Args[0], "-test.run=^TestWebHelperProcess$", "--", duration.String()}
}

func TestWebHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WEB_HELPER_PROCESS") != "1" {
		return
	}
	duration, err := time.ParseDuration(os.Args[len(os.Args)-1])
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(duration)
}
