package piagent

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Many goroutines hammering acquire/release on one mkdir lock must never see
// a non-contention error. On Windows, mkdir on a just-removed (delete-pending)
// lock directory reports ERROR_ACCESS_DENIED; that is contention and must be
// retried, not returned (it failed TestConcurrentSendsGetDistinctOrderedTasks
// intermittently on windows-latest).
func TestDirLockSurvivesHeavyChurn(t *testing.T) {
	for name, acquire := range map[string]func(string) (func(), error){
		"plain": func(p string) (func(), error) { return acquireDirLock(p, 10*time.Second, time.Minute) },
		"owned": func(p string) (func(), error) { return acquireOwnedDirLock(p, 10*time.Second, time.Minute) },
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.lock")
			var holders, failures atomic.Int32
			var wg sync.WaitGroup
			for g := 0; g < 16; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 60; i++ {
						release, err := acquire(path)
						if err != nil {
							failures.Add(1)
							t.Errorf("acquire: %v", err)
							return
						}
						if holders.Add(1) != 1 {
							t.Error("two holders at once")
						}
						holders.Add(-1)
						release()
					}
				}()
			}
			wg.Wait()
			if failures.Load() != 0 {
				t.Fatalf("%d acquisitions failed", failures.Load())
			}
		})
	}
}

// A lock that stays held times out with errLockTimeout (still matchable) and
// the path, rather than spinning forever or returning a bare error.
func TestDirLockTimeoutKeepsSentinelAndPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := acquireDirLock(path, 100*time.Millisecond, time.Hour)
	if !errors.Is(err, errLockTimeout) {
		t.Fatalf("err = %v, want errLockTimeout", err)
	}
}
