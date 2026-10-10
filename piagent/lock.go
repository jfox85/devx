package piagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
			return func() { releaseDirLock(path, os.Remove) }, nil
		}
		if !lockContended(path, err) {
			return nil, fmt.Errorf("acquire lock %s: %w", path, err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > stale {
			// Abandoned by a crashed holder; remove and retry immediately
			// (if the removal fails, fall through to the deadline and sleep).
			if os.Remove(path) == nil {
				continue
			}
		}
		if time.Now().After(deadline) {
			// Keep the last mkdir error: on Windows a persistent permission
			// problem is retried like contention and surfaces here.
			return nil, fmt.Errorf("%w: %s (last error: %v)", errLockTimeout, path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// lockReleaseRetry bounds how long a release keeps retrying the removal.
const lockReleaseRetry = 2 * time.Second

// releaseDirLock removes a held lock directory, retrying transient failures.
// A release that silently fails leaks the lock until the stale timeout, so
// every waiter times out. On Windows removal fails transiently while another
// process or goroutine has the directory (or its pid file) open for a moment
// to probe the lock; on Unix the first attempt succeeds.
func releaseDirLock(path string, remove func(string) error) {
	deadline := time.Now().Add(lockReleaseRetry)
	for {
		err := remove(path)
		if err == nil || os.IsNotExist(err) || time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// releaseOwnedDirLock releases an owned lock in two steps: remove our pid
// file, then the (now empty) directory. Retrying a recursive removal could
// delete a lock that a waiter reclaimed in between (it saw no pid file); a
// plain directory removal instead fails on the new holder's pid file.
func releaseOwnedDirLock(path string) {
	releaseDirLock(filepath.Join(path, "pid"), os.Remove)
	releaseDirLock(path, os.Remove)
}

// AcquireOwnedLock takes a Go-only, cross-process mkdir lock at path (with
// crashed-owner recovery). Used by optional tool providers such as the
// artifact bridge for state that the Node bridge never touches.
func AcquireOwnedLock(path string) (func(), error) {
	return acquireOwnedDirLock(path, agentLockTimeout, agentLockStale)
}

// acquireOwnedDirLock is acquireDirLock plus a pid file, so a lock left by a
// crashed process (e.g. an MCP server killed mid-start) is reclaimed as soon
// as its owner is dead instead of after the stale timeout. Used only for
// Go-only locks; the agent lock shared with the Node bridge stays a plain
// mkdir lock.
func acquireOwnedDirLock(path string, timeout, stale time.Duration) (func(), error) {
	deadline := time.Now().Add(timeout)
	for {
		err := os.Mkdir(path, 0o700)
		if err == nil {
			_ = os.WriteFile(filepath.Join(path, "pid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
			return func() { releaseOwnedDirLock(path) }, nil
		}
		if !lockContended(path, err) {
			return nil, fmt.Errorf("acquire lock %s: %w", path, err)
		}
		info, statErr := os.Stat(path)
		if statErr == nil {
			data, rerr := os.ReadFile(filepath.Join(path, "pid"))
			pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			ownerDead := rerr == nil && pid > 0 && pid != os.Getpid() && !processAlive(pid)
			// No pid file yet: the owner is between mkdir and write; give
			// it a moment before treating it as abandoned.
			noPidTooLong := os.IsNotExist(rerr) && time.Since(info.ModTime()) > 2*time.Second
			if ownerDead || noPidTooLong || time.Since(info.ModTime()) > stale {
				if os.RemoveAll(path) == nil {
					continue
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%w: %s (last error: %v)", errLockTimeout, path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
