package rate

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/util"

	"gopkg.in/yaml.v3"
)

type RateError struct {
	BlockUntil time.Duration // Advisory block duration to the server.
	RetryAfter time.Duration // Advisory retry duration to the client.
	NoHeader   bool          // Whether to suppress the Retry-After header.
}

func (e RateError) AdviseClient(now time.Time) (retryAfter time.Time, ok bool) {
	if e.NoHeader {
		return
	}
	if e.RetryAfter > 0 {
		retryAfter = now.Add(e.RetryAfter * 2)
		ok = true
	}
	return
}

func (e RateError) Error() string {
	if e.RetryAfter > 0 && !e.NoHeader {
		return fmt.Sprintf("too many requests, retry after %s", e.RetryAfter)
	}
	return "too many requests"
}

// Limit context is a sliding window counter with backpressure, used to
// track the rate of events and the current wait queue length.
type LimitCounter struct {
	slidingWindow              // Sliding window counter.
	queue         atomic.Int32 // Backpressure queue length.
	blockWindow   window       // Fixed window for block duration.
}

func (c *LimitCounter) Reset() {
	c.slidingWindow.Reset()
	c.blockWindow.Reset()
}

// Limit defines the options for a rate limiter.
type Options struct {
	ID           string        `yaml:"id,omitempty"`          // Optional identifier.
	Rate         Rate          `yaml:"rate"`                  // The rate to enforce.
	Burst        int32         `yaml:"burst,omitempty"`       // The maximum burst size.
	BlockAfter   Rate          `yaml:"block_after,omitempty"` // The rate (of excessive requests) after which block duration is advised.
	BlockFor     util.Duration `yaml:"block_for,omitempty"`   // The duration to block the client.
	AdviseClient bool          `yaml:"advise,omitempty"`      // Whether to advise the client on the block duration.
}
type Limit struct {
	Options
}

func NewLimit(o Options) Limit {
	return Limit{o}
}

func (l *Limit) validate() error {
	if l.Rate.Period < 0 {
		return errors.New("rate must be specified")
	}
	if l.Burst < 0 {
		return errors.New("burst must be non-negative")
	}
	if !l.Rate.IsPositive() {
		l.Rate = Rate{}
	}
	return nil
}
func (l *Limit) IsZero() bool {
	return !l.Rate.IsPositive()
}
func (l *Limit) UnmarshalJSON(data []byte) error {
	return yaml.Unmarshal(data, l)
}
func (l *Limit) UnmarshalYAML(node *yaml.Node) (err error) {
	if node.Kind != yaml.ScalarNode {
		var tmp Options
		if err = node.Decode(&tmp); err != nil {
			return
		}
		l.Options = tmp
		return l.validate()
	} else {
		var text string
		if err = node.Decode(&text); err != nil {
			return
		}
		return l.UnmarshalText([]byte(text))
	}
}
func (l *Limit) UnmarshalText(text []byte) (err error) {
	pieces := strings.Fields(string(text))
	if len(pieces) == 0 {
		return errors.New("invalid limit format")
	}

	if before, ok := strings.CutPrefix(pieces[0], "@"); ok {
		l.ID = before
		pieces = pieces[1:]
	}

	l.Rate, err = Parse(pieces[0])
	if err != nil {
		return
	}
	pieces = pieces[1:]

	for changed := true; changed; {
		changed = false
		for i, p := range pieces {
			if before, ok := strings.CutPrefix(p, "burst="); ok {
				if n, err := fmt.Sscanf(before, "%d", &l.Burst); err != nil || n != 1 {
					return errors.New("invalid burst format")
				}
			} else if before, ok = strings.CutPrefix(p, "block_after="); ok {
				if err := l.BlockAfter.UnmarshalText([]byte(before)); err != nil {
					return err
				}
			} else if before, ok = strings.CutPrefix(p, "block_for="); ok {
				if err := l.BlockFor.UnmarshalText([]byte(before)); err != nil {
					return err
				}
			} else if before, ok = strings.CutPrefix(p, "advise="); ok {
				if l.AdviseClient, err = strconv.ParseBool(before); err != nil {
					return err
				}
			} else {
				break
			}
			pieces = append(pieces[:i], pieces[i+1:]...)
			changed = true
			break
		}
	}
	if len(pieces) > 0 {
		return errors.New("invalid limit format")
	}
	if err == nil {
		err = l.validate()
	}
	return
}
func (l *Limit) String() string {
	res := []string{}
	if l.ID != "" {
		res = append(res, "@"+l.ID)
	}
	res = append(res, l.Rate.String())
	if l.Burst > 0 {
		res = append(res, fmt.Sprintf("burst=%d", l.Burst))
	}
	return strings.Join(res, " ")
}

type limitKey struct {
	id string
	r  int64
	b  int64
}

func (l *Limit) tokey() limitKey {
	return limitKey{l.ID, int64(l.Rate.Period), int64(l.BlockAfter.Period)}
}

// Returns the advisory error for exceeding the limit.
func (l *Limit) OnExceed(lc *LimitCounter) (e RateError) {
	e.RetryAfter = l.Rate.Interval()
	if l.Burst > 0 {
		e.RetryAfter += e.RetryAfter * time.Duration(lc.queue.Load())
	}
	if e.RetryAfter < 0 {
		e.RetryAfter = 0
	}
	if l.BlockFor.Duration() > 0 {
		if l.BlockAfter.IsZero() || !lc.blockWindow.Inc(Ticks(l.BlockAfter.Period), Count(l.BlockAfter.Count)) {
			e.BlockUntil = l.BlockFor.Duration()
		}
	}
	e.NoHeader = !l.AdviseClient
	return
}

// Enforces a limit given the context and counter.
func (l *Limit) Enforce(ctx context.Context, ctr *LimitCounter) error {
	// Try to bypass the queue first.
	bp := ctr.queue.Load()
	if bp == 0 {
		lim := Count(l.Rate.Count)
		if lim == 0 {
			lim = maxCounter
		}
		if ctr.Inc(Subticks(l.Rate.Period), lim) {
			return nil
		} else if l.Burst <= 0 {
			return l.OnExceed(ctr)
		}
	} else if bp >= l.Burst {
		return l.OnExceed(ctr)
	}

	// If the queue is full, fail.
	bp = ctr.queue.Add(1)
	defer ctr.queue.Add(-1)
	if bp >= l.Burst {
		return l.OnExceed(ctr)
	}

	// Determine the delay, if it exceeds the deadline, fail.
	delay := l.Rate.Interval() * time.Duration(bp+1)
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) < delay {
			return context.DeadlineExceeded
		}
	}

	// Wait for the delay or the context to be canceled.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case t := <-time.After(delay):
		// Forcibly increment the counter and return success.
		ctr.Inc(ToSubticks(t, l.Rate.Period), maxCounter)
		return nil
	}
}

// Returns the counter for the given limit, creating it if necessary.
func (l *Limit) GetCounters(s *sync.Map) *LimitCounter {
	k := l.tokey()
	v, ok := s.Load(k)
	if !ok {
		v, _ = s.LoadOrStore(k, &LimitCounter{})
	}
	return v.(*LimitCounter)
}

// Enforces a limit given the context and sync.Map to store counters per client.
func (l Limit) EnforceVar(ctx context.Context, s *sync.Map) error {
	return l.Enforce(ctx, l.GetCounters(s))
}

// Limiter is a rate limiter with its own local counter.
type Limiter struct {
	Limit
	Counter *LimitCounter
}

func LocalLimiter(l Options) Limiter {
	return Limiter{Limit: Limit{l}, Counter: new(LimitCounter)}
}
func (l *Limiter) Enforce(ctx context.Context) error {
	return l.Limit.Enforce(ctx, l.Counter)
}
func (l *Limiter) Read() Count {
	return l.Counter.Read(Subticks(l.Rate.Period), Count(l.Rate.Count))
}
func (l *Limiter) Reset() {
	l.Counter.Reset()
}
