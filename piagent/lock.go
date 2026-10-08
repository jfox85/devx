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
			return func() { _ = os.RemoveAll(path) }, nil
		}
		if !os.IsExist(err) {
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
				_ = os.RemoveAll(path)
				continue
			}
		}
		if time.Now().After(deadline) {
			return nil, errLockTimeout
		}
		time.Sleep(20 * time.Millisecond)
	}
}
