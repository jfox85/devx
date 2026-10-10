package piagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
						time.Sleep(50 * time.Microsecond) // give an overlap a chance to show
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
	if !errors.Is(err, errLockTimeout) || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want errLockTimeout naming %s", err, path)
	}
}

// A release whose first removal attempt fails transiently (as on Windows when
// a waiter is probing the lock) must keep trying, not leak the lock.
func TestReleaseDirLockRetriesTransientFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.lock")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	releaseDirLock(path, func(p string) error {
		calls++
		if calls < 3 {
			return errors.New("transient sharing violation")
		}
		return os.Remove(p)
	})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock leaked after release (calls=%d): %v", calls, err)
	}
}

// Releasing an owned lock must not delete a lock that someone else holds at
// that path by the time a retry runs: only our pid file and then an empty
// directory are removed.
func TestOwnedReleaseNeverRemovesAnotherHoldersLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.lock")
	release, err := acquireOwnedDirLock(path, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a waiter that reclaimed the lock in between: a new pid file
	// appears after ours is gone (here: our pid file replaced by theirs).
	if err := os.WriteFile(filepath.Join(path, "pid"), []byte("424242"), 0o600); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(path, "holder-marker")
	if err := os.WriteFile(extra, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(extra); err != nil {
		t.Fatalf("release removed another holder's lock contents: %v", err)
	}
}
