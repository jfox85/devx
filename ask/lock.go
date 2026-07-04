package ask

import (
	"encoding/json"
	"fmt"
	"os"
	"syscall"
	"time"
)

const staleLockAfter = 30 * time.Minute

type lockFile struct {
	PID       int       `json:"pid"`
	CreatedAt time.Time `json:"created_at"`
}

func acquireFileLock(path string) (func(), error) {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if isStaleLock(path) {
			_ = os.Remove(path)
			lock, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		}
		if err != nil {
			return nil, fmt.Errorf("lock %s is held: %w", path, err)
		}
	}
	_ = json.NewEncoder(lock).Encode(lockFile{PID: os.Getpid(), CreatedAt: time.Now().UTC()})
	_ = lock.Close()
	return func() { _ = os.Remove(path) }, nil
}

func isStaleLock(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var info lockFile
	if err := json.Unmarshal(data, &info); err != nil {
		stat, statErr := os.Stat(path)
		return statErr == nil && time.Since(stat.ModTime()) > staleLockAfter
	}
	if time.Since(info.CreatedAt) > staleLockAfter {
		return true
	}
	if info.PID <= 0 {
		return false
	}
	process, err := os.FindProcess(info.PID)
	if err != nil {
		return true
	}
	return process.Signal(syscall.Signal(0)) != nil
}
