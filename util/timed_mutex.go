package util

import (
	"context"
	"sync/atomic"
	"time"
)

type TimedMutex struct {
	ch atomic.Value // chan struct{}
}

func (tm *TimedMutex) ref() chan struct{} {
	if v := tm.ch.Load(); v != nil {
		return v.(chan struct{})
	}
	ch := make(chan struct{}, 1)
	if tm.ch.CompareAndSwap(nil, ch) {
		return ch
	}
	close(ch)
	return tm.ch.Load().(chan struct{})
}

func (tm *TimedMutex) Lock() {
	tm.ref() <- struct{}{}
}
func (tm *TimedMutex) Unlock() {
	<-tm.ref()
}

func after(d time.Duration) <-chan time.Time {
	if d <= 0 {
		return nil
	}
	return time.After(d)
}

func (tm *TimedMutex) TryLock(d time.Duration) bool {
	select {
	case tm.ref() <- struct{}{}:
		return true
	case <-after(d):
		return false
	}
}
func (tm *TimedMutex) TryLockContext(ctx context.Context) bool {
	select {
	case tm.ref() <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}
