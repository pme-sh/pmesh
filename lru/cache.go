package lru

import (
	"sync"
	"sync/atomic"
	"time"
)

type Entry[V any] struct {
	Value   V
	lastUse atomic.Int64
	nAcq    atomic.Int64
}

func (e *Entry[V]) Acquire() {
	e.nAcq.Add(1)
}
func (e *Entry[V]) Release() {
	e.Bump()
	e.nAcq.Add(-1)
}
func (e *Entry[V]) Bump() {
	e.lastUse.Store(time.Now().UnixMilli())
}
func (e *Entry[V]) Expired(threshold time.Time) bool {
	if e.nAcq.Load() == 0 {
		return e.lastUse.Load() < threshold.UnixMilli()
	}
	return false
}

type Cache[K comparable, V any] struct {
	Expiry          time.Duration
	CleanupInterval time.Duration
	New             func(K, *Entry[V]) error
	Evict           func(K, V)
	Singleflight    bool

	underlying    sync.Map // map[K]*lruWrapper[V]
	itemCount     atomic.Int64
	cleanupTicker atomic.Pointer[time.Ticker]
	creationLock  sync.Mutex
}

func (c *Cache[K, V]) Delete(key K) {
	v, ok := c.underlying.LoadAndDelete(key)
	if ok {
		c.onDelete(key, v.(*Entry[V]).Value)
	}
}

func (c *Cache[K, V]) Cleanup() {
	threshold := time.Now().Add(-c.Expiry)
	c.underlying.Range(func(key, value any) bool {
		wrapper := value.(*Entry[V])
		if wrapper.Expired(threshold) {
			if c.underlying.CompareAndDelete(key, value) {
				c.onDelete(key.(K), wrapper.Value)
			}
		}
		return true
	})
}

func (c *Cache[K, V]) onDelete(k K, v V) {
	c.itemCount.Add(-1)
	if c.Evict != nil {
		c.Evict(k, v)
	}
}
func (c *Cache[K, V]) onInsert() {
	if c.itemCount.Add(1) == 1 && c.CleanupInterval > 0 {
		ticker := time.NewTicker(c.CleanupInterval)
		if prev := c.cleanupTicker.Swap(ticker); prev != nil {
			prev.Stop()
		}
		go func() {
			defer ticker.Stop()
			for range ticker.C {
				c.Cleanup()
				if c.itemCount.Load() == 0 {
					c.cleanupTicker.CompareAndSwap(ticker, nil)
					return
				}
			}
		}()
	}
}

func (c *Cache[K, V]) ReplaceEntry(key K, v *Entry[V]) {
	v.Bump()
	prev, ok := c.underlying.Swap(key, v)
	if !ok {
		c.onInsert()
	} else if c.Evict != nil && prev != v {
		c.Evict(key, prev.(*Entry[V]).Value)
	}
}
func (c *Cache[K, V]) SetEntry(key K, v *Entry[V]) (result *Entry[V], ok bool) {
	v.Bump()
	actual, loaded := c.underlying.LoadOrStore(key, v)
	if !loaded {
		c.onInsert()
		return v, true
	} else {
		return actual.(*Entry[V]), false
	}
}
func (c *Cache[K, V]) GetEntryIf(key K) (value *Entry[V], ok bool) {
	v, ok := c.underlying.Load(key)
	if ok {
		value = v.(*Entry[V])
		value.Bump()
	}
	return
}
func (c *Cache[K, V]) GetEntry(key K) (value *Entry[V], err error) {
	v, ok := c.underlying.Load(key)
	if ok {
		value = v.(*Entry[V])
		value.Bump()
		return
	} else if c.New == nil {
		return
	}

	if c.Singleflight {
		c.creationLock.Lock()
		defer c.creationLock.Unlock()
		if v, ok = c.underlying.Load(key); ok {
			value = v.(*Entry[V])
			value.Bump()
			return
		}
	}

	value = &Entry[V]{}
	err = c.New(key, value)
	if err != nil {
		return
	}

	if c.Singleflight {
		c.ReplaceEntry(key, value)
	} else {
		value, _ = c.SetEntry(key, value)
	}
	return
}

func (c *Cache[K, V]) Set(key K, value V) (result V, ok bool) {
	r, ok := c.SetEntry(key, &Entry[V]{Value: value})
	return r.Value, ok
}
func (c *Cache[K, V]) Replace(key K, value V) {
	c.ReplaceEntry(key, &Entry[V]{Value: value})
}
func (c *Cache[K, V]) GetIf(key K) (value V, ok bool) {
	v, ok := c.GetEntryIf(key)
	if ok {
		value = v.Value
	}
	return
}
func (c *Cache[K, V]) Get(key K) (value V, err error) {
	v, err := c.GetEntry(key)
	if err == nil {
		value = v.Value
	}
	return
}
