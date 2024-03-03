package concurrent

import "sync/atomic"

type Set[K comparable] struct {
	raw Map[K, struct{}]
	len atomic.Int64
}

func (s *Set[K]) Add(key K) (added bool) {
	if _, loaded := s.raw.LoadOrStore(key, struct{}{}); !loaded {
		s.len.Add(1)
		return true
	}
	return false
}
func (s *Set[K]) Delete(key K) bool {
	_, loaded := s.raw.LoadAndDelete(key)
	if loaded {
		s.len.Add(-1)
	}
	return loaded
}
func (s *Set[K]) Has(key K) (ok bool) {
	_, ok = s.raw.Load(key)
	return
}
func (s *Set[K]) Range(f func(key K) bool) {
	s.raw.Range(func(key K, _ struct{}) bool {
		return f(key)
	})
}
func (s *Set[K]) Len() int {
	return int(s.len.Load())
}
func (s *Set[K]) All() (all []K) {
	s.Range(func(key K) bool {
		all = append(all, key)
		return true
	})
	return
}
