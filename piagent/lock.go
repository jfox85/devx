package piagent

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// The agent lock is shared between this Go process and the Pi bridge
// extension (Node). mkdir is atomic on POSIX filesystems in both runtimes, so
// it is the one primitive both sides can use without native flock bindings.
// Every lease change, delivery decision, cancellation and event append happens
// while holding it, which is what makes takeover a real fence: once a
// takeover returns, no dispatch that was not already marked delivered can be
// delivered until a human releases control.
const (
	agentLockName    = "agent.lock"
	agentLockTimeout = 5 * time.Second
	// A lock older than this is treated as abandoned by a crashed holder.
	// Critical sections are a handful of small file operations.
	agentLockStale = 10 * time.Second
)

var errLockTimeout = errors.New("timed out waiting for agent lock")

func acquireDirLock(path string, timeout, stale time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	for {
		err := os.Mkdir(path, 0o700)
		if err == nil {
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("acquire lock %s: %w", path, err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > stale {
			// Abandoned by a crashed holder; remove and retry immediately.
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errLockTimeout
		}
		time.Sleep(10 * time.Millisecond)
	}
}
