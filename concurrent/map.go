package concurrent

import "sync"

type Map[K comparable, V any] struct {
	raw sync.Map
}

func (m *Map[K, V]) CompareAndDelete(key K, old V) (deleted bool) {
	deleted = m.raw.CompareAndDelete(key, old)
	return
}
func (m *Map[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	if v, loaded := m.raw.LoadAndDelete(key); !loaded {
		return value, false
	} else {
		return v.(V), true
	}
}
func (m *Map[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	if v, loaded := m.raw.LoadOrStore(key, value); !loaded {
		return value, false
	} else {
		return v.(V), true
	}
}
func (m *Map[K, V]) Swap(key K, value V) (previous V, loaded bool) {
	if v, loaded := m.raw.Swap(key, value); !loaded {
		return value, false
	} else {
		return v.(V), true
	}
}
func (m *Map[K, V]) Load(key K) (value V, ok bool) {
	if v, loaded := m.raw.Load(key); !loaded {
		return
	} else {
		return v.(V), true
	}
}
func (m *Map[K, V]) Delete(key K) {
	_, _ = m.LoadAndDelete(key)
}
func (m *Map[K, V]) Store(key K, value V) {
	_, _ = m.Swap(key, value)
}
func (m *Map[K, V]) CompareAndSwap(key K, old, new V) bool {
	return m.raw.CompareAndSwap(key, old, new)
}
func (m *Map[K, V]) Range(f func(key K, value V) bool) {
	m.raw.Range(func(key, value any) bool {
		return f(key.(K), value.(V))
	})
}
func (m *Map[K, V]) All() (all map[K]V) {
	all = make(map[K]V)
	m.Range(func(key K, value V) bool {
		all[key] = value
		return true
	})
	return
}
func (m *Map[K, V]) Keys() (keys []K) {
	keys = []K{}
	m.Range(func(key K, value V) bool {
		keys = append(keys, key)
		return true
	})
	return
}
func (m *Map[K, V]) Values() (values []V) {
	values = []V{}
	m.Range(func(key K, value V) bool {
		values = append(values, value)
		return true
	})
	return
}
