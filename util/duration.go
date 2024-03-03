package util

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func NewDuration(d any) Duration {
	switch d := d.(type) {
	case Duration:
		return d
	case time.Duration:
		return Duration(d)
	case string:
		dur, _ := time.ParseDuration(d)
		return Duration(dur)
	default:
		panic(fmt.Errorf("unsupported type: %T", d))
	}
}

var closed = make(chan time.Time)

func init() {
	close(closed)
}

func (d Duration) IsZero() bool {
	return d == 0
}
func (d Duration) IsPositive() bool {
	return d > 0
}
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// If positive duration, returns a channel that will be given the current time after the duration has passed.
// If non-positive duration, returns a closed channel, which will be selected immediately.
func (d Duration) After() <-chan time.Time {
	if !d.IsPositive() {
		return closed
	}
	return time.After(time.Duration(d))
}

// If positive duration, returns a channel that will be given the current time after the duration has passed.
// If non-positive duration, returns nil, which will never be selected.
func (d Duration) AfterIf() <-chan time.Time {
	if !d.IsPositive() {
		return nil
	}
	return time.After(time.Duration(d))
}

// Creates a context that will be cancelled after the duration has passed.
func (d Duration) TimeoutCause(ctx context.Context, cause error) (context.Context, context.CancelFunc) {
	if !d.IsPositive() {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, time.Duration(d), cause)
}
func (d Duration) Timeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return d.TimeoutCause(ctx, nil)
}

// Clamps the duration to the given range.
func (d Duration) Clamp(lmin time.Duration, lmax time.Duration) Duration {
	return Duration(max(min(time.Duration(d), lmax), lmin))
}
func (d Duration) Min(o time.Duration) Duration {
	return Duration(min(time.Duration(d), o))
}
func (d Duration) Max(o time.Duration) Duration {
	return Duration(max(time.Duration(d), o))
}
func (d Duration) Or(o time.Duration) Duration {
	if d.IsZero() {
		return Duration(o)
	} else {
		return Duration(max(0, d))
	}
}

// String representation of the duration.
func (d Duration) String() string {
	return time.Duration(d).String()
}
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}
func (d *Duration) UnmarshalText(text []byte) (err error) {
	if bytes.Equal(text, []byte("never")) {
		*d = -1
		return
	}
	dx, err := time.ParseDuration(string(text))
	*d = Duration(dx)
	return
}

func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var res string
	if err := node.Decode(&res); err != nil {
		return err
	}
	return d.UnmarshalText([]byte(res))
}
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(d.String()), nil
}
func (d *Duration) UnmarshalJSON(text []byte) (err error) {
	var res string
	if err := json.Unmarshal(text, &res); err != nil {
		return err
	}
	return d.UnmarshalText([]byte(res))
}

func (d Duration) Display() string {
	x := time.Duration(d)
	if x < time.Second {
		x = x.Round(time.Second / 100)
	} else {
		x = x.Round(time.Second)
	}
	return x.String()
}
