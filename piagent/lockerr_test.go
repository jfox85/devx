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
// a non-contention error, never overlap, and never leak the lock. On Windows,
// mkdir on a just-removed (delete-pending) lock directory reports
// ERROR_ACCESS_DENIED (contention, must be retried), and a release can fail
// transiently while a waiter probes the lock (must be retried, not leaked).
//
// The lock is an unfair spin lock, so under full-suite load one goroutine can
// wait a long time behind 15 others (seen on windows-latest against a 10s
// per-acquire limit). Waiting is therefore bounded per acquire generously,
// while a leak is caught fast instead: a watchdog fails the test if the lock
// directory stays in place with no holder for longer than leakAfter. (A
// release that takes longer than that, e.g. all three retry phases, also
// counts: it would break the production 5s agent-lock timeout too.)
func TestDirLockSurvivesHeavyChurn(t *testing.T) {
	const timeout, stale, leakAfter = time.Minute, 5 * time.Minute, 3 * time.Second
	for name, acquire := range map[string]func(string) (func(), error){
		"plain": func(p string) (func(), error) { return acquireDirLock(p, timeout, stale) },
		"owned": func(p string) (func(), error) { return acquireOwnedDirLock(p, timeout, stale) },
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.lock")
			var holders, failures atomic.Int32
			var wg sync.WaitGroup
			stop := make(chan struct{})
			leaked := make(chan struct{})
			go func() { // leak watchdog
				var since time.Time
				for {
					select {
					case <-stop:
						return
					case <-time.After(20 * time.Millisecond):
					}
					_, err := os.Stat(path)
					if err == nil && holders.Load() == 0 {
						if since.IsZero() {
							since = time.Now()
						} else if time.Since(since) > leakAfter {
							close(leaked)
							return
						}
					} else {
						since = time.Time{}
					}
				}
			}()
			for g := 0; g < 16; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < 60; i++ {
						select {
						case <-leaked:
							return
						default:
						}
						release, err := acquire(path)
						if err != nil {
							failures.Add(1)
							age := "absent"
							if fi, serr := os.Stat(path); serr == nil {
								age = time.Since(fi.ModTime()).String()
							}
							pid, _ := os.ReadFile(filepath.Join(path, "pid"))
							t.Errorf("acquire: %v (lock dir age %s, pid %q, holders %d)", err, age, pid, holders.Load())
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
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-leaked:
				// Fail without abandoning goroutines that are still waiting
				// to acquire: clear the leaked lock so they finish, then wait.
				t.Errorf("lock directory left in place with no holder for over %v (leaked release)", leakAfter)
				_ = os.RemoveAll(path)
				<-done
				return
			}
			close(stop)
			if failures.Load() != 0 {
				t.Fatalf("%d acquisitions failed", failures.Load())
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("lock left behind after every holder released: %v", err)
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

// Releasing an owned lock must not delete a lock that a waiter reclaimed in
// the meantime: if the pid file is not ours, release leaves it alone.
func TestOwnedReleaseNeverRemovesAnotherHoldersLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.lock")
	release, err := acquireOwnedDirLock(path, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// A waiter reclaimed the lock: the directory now holds only its pid.
	theirs := filepath.Join(path, "pid")
	if err := os.WriteFile(theirs, []byte("424242"), 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	if data, err := os.ReadFile(theirs); err != nil || string(data) != "424242" {
		t.Fatalf("release removed another holder's lock: %q %v", data, err)
	}
}

// Normal release removes our own owned lock completely.
func TestOwnedReleaseRemovesOwnLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.lock")
	release, err := acquireOwnedDirLock(path, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("own lock not released: %v", err)
	}
}

// The persistence tracker never fires for errors that are not ACCESS_DENIED
// (so Unix behaviour is unchanged).
func TestDeniedTrackerIgnoresOtherErrors(t *testing.T) {
	var d deniedTracker
	d.since = time.Now().Add(-time.Hour)
	if d.persistent(os.ErrExist) || !d.since.IsZero() {
		t.Fatal("non-ACCESS_DENIED errors must reset and never be persistent")
	}
}

// The owned-lock release must not give up on a transient pid read failure
// (on Windows a probing waiter can hold the pid file open): readFileRetry
// retries other errors until the deadline; a missing file returns at once.
func TestReadFileRetryRecoversFromTransientFailure(t *testing.T) {
	calls := 0
	flaky := func(string) ([]byte, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("transient sharing violation")
		}
		return []byte("1"), nil
	}
	data, err := readFileRetry("pid", 2*time.Second, flaky)
	if err != nil || string(data) != "1" || calls != 3 {
		t.Fatalf("readFileRetry = %q, %v after %d calls; want success on the 3rd", data, err, calls)
	}
	calls = 0
	missing := func(string) ([]byte, error) { calls++; return nil, os.ErrNotExist }
	if _, err := readFileRetry("pid", 2*time.Second, missing); !os.IsNotExist(err) || calls != 1 {
		t.Fatalf("missing file: %v after %d calls; want NotExist at once", err, calls)
	}
}
