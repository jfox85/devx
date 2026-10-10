package session

import "sync"

// holdSessionsFileOpenForTest opens path exactly the way the lock-free
// reader does and keeps it open until release is called (idempotent).
func holdSessionsFileOpenForTest(path string) (release func(), err error) {
	f, err := openSessionsFileForRead(path)
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { _ = f.Close() }) }, nil
}
