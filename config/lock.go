package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/gofrs/flock"
)

func makeLockFile() *flock.Flock {
	return flock.New(filepath.Join(Home(), "session.lock"))
}
func writePidFile(locked bool) {
	if locked {
		os.WriteFile(filepath.Join(Home(), "session.pid"), []byte(strconv.Itoa(os.Getpid())), 0644)
	} else {
		os.Remove(filepath.Join(Home(), "session.pid"))
	}
}

var lockFile *flock.Flock
var lockCount int
var lockMutex sync.Mutex

func tryLockLocked() (err error) {
	if lockFile == nil {
		lockFile = makeLockFile()
		lockCount = 0
	}

	if lockCount == 0 {
		var ok bool
		ok, err = lockFile.TryLock()
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("failed to lock file")
		}
		writePidFile(true)
	}

	lockCount++
	return nil
}
func unlockLocked() {
	lockCount--
	if lockCount < 0 {
		panic("lock count < 0")
	}

	if lockCount == 0 {
		writePidFile(false)
		lockFile.Unlock()
		lockFile.Close()
		lockFile = nil
	}
}

func Unlock() {
	lockMutex.Lock()
	defer lockMutex.Unlock()
	unlockLocked()
}
func TryLock() (err error) {
	lockMutex.Lock()
	defer lockMutex.Unlock()
	return tryLockLocked()
}

func WithLock(f func() error) error {
	lockMutex.Lock()
	defer lockMutex.Unlock()
	err := tryLockLocked()
	if err != nil {
		return err
	}
	defer unlockLocked()
	return f()
}
