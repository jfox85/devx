package session

import (
	"io"
	"os"
	"sync"
)

// holdSessionsFileOpenForTest opens path exactly the way the lock-free
// reader does and keeps it open until release is called (idempotent).
func holdSessionsFileOpenForTest(path string) (f *os.File, release func(), err error) {
	f, err = openSessionsFileForRead(path)
	if err != nil {
		return nil, nil, err
	}
	var once sync.Once
	return f, func() { once.Do(func() { _ = f.Close() }) }, nil
}

func readAllForTest(f *os.File) ([]byte, error) { return io.ReadAll(f) }
