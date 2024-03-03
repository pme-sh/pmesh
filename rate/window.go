package rate

import (
	"sync/atomic"
	"time"
)

type Count = uint32
type Subtick uint64
type Tick Count

const (
	maxCounter            = ^Count(0)
	slidingPrecisionShift = 8
	slidingPrecisionMask  = 1<<slidingPrecisionShift - 1
)

func ToTicks(time time.Time, dur time.Duration) Tick {
	return Tick(time.UnixNano() / dur.Nanoseconds())
}
func ToSubticks(time time.Time, dur time.Duration) Subtick {
	tm := Subtick(time.UnixNano()) << slidingPrecisionShift
	return tm / Subtick(dur.Nanoseconds())
}
func Ticks(dur time.Duration) Tick {
	return ToTicks(time.Now(), dur)
}
func Subticks(dur time.Duration) Subtick {
	return ToSubticks(time.Now(), dur)
}
func (s Subtick) Tick() Tick {
	return Tick(s >> slidingPrecisionShift)
}
func (s Subtick) Carry(prev, limit Count) Count {
	coeff := (^s) & slidingPrecisionMask
	return min(limit, Count((uint64(prev)*uint64(coeff))>>slidingPrecisionShift))
}

type window struct {
	v atomic.Uint64 // lo: tick | hi: count
}

func (w *window) Reset() {
	w.v.Store(0)
}
func (w *window) Read(tick Tick) Count {
	v := w.v.Load()
	count, n := Count(v>>32), Tick(v)
	if n != tick {
		return 0
	}
	return count
}
func (w *window) Inc(now Tick, limit Count) (ok bool) {
	for {
		expected := w.v.Load()
		count, tick := Count(expected>>32), Tick(expected)
		if tick < now {
			count = 0
			tick = now
		}
		if count >= limit {
			return false
		}
		count++
		desired := uint64(count)<<32 | uint64(now)
		if w.v.CompareAndSwap(expected, desired) {
			return true
		}
	}
}

type slidingWindow struct {
	window [2]window
}

func (w *slidingWindow) Reset() {
	w.window[0].Reset()
	w.window[1].Reset()
}
func (w *slidingWindow) Inc(subtick Subtick, limit Count) (ok bool) {
	tick := subtick.Tick()
	i := tick & 1
	count := subtick.Carry(w.window[i^1].Read(tick), limit>>1)
	if count >= limit {
		return
	}
	return w.window[i].Inc(tick, limit-count)
}
func (w *slidingWindow) Read(subtick Subtick, limit Count) (count Count) {
	tick := subtick.Tick()
	i := tick & 1
	count = subtick.Carry(w.window[i^1].Read(tick), limit>>1)
	count += w.window[i].Read(tick)
	return
}
