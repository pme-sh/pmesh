package ui

import (
	"cmp"
	"io"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/samber/lo"
)

const updateRateLimit = time.Second / 15

type Store[T any] interface {
	Next() (data T, next Store[T], err error)
}
type ImmutableStore[T any] [1]T

func (d ImmutableStore[T]) Next() (data T, next Store[T], err error) {
	return d[0], nil, nil
}

type ChanStore[T any] <-chan T

func (c ChanStore[T]) Next() (data T, next Store[T], err error) {
	data, ok := <-c
	if ok {
		next = c
	} else {
		err = io.EOF
	}
	return
}

type PullStore[T any] func() (data T, err error)

func (s PullStore[T]) Next() (data T, next Store[T], err error) {
	data, err = s()
	if err != io.EOF {
		next = s
	}
	return
}

type DerivedStore[T, U any] struct {
	Src Store[T]
	Fn  func(T) U
}

func (t DerivedStore[T, U]) Next() (data U, next Store[U], err error) {
	dataT, nextSrc, err := t.Src.Next()
	if err == nil {
		data = t.Fn(dataT)
	}
	if nextSrc != nil {
		t.Src = nextSrc
		next = t
	}
	return
}

type Observer[T any] struct {
	Source Store[T]
	Data   T
	Err    error
	Busy   bool
}

func (d *Observer[T]) refresh(deferdur time.Duration) tea.Cmd {
	src := d.Source
	if src == nil {
		return nil
	}
	d.Source = nil
	return func() tea.Msg {
		time.Sleep(deferdur)
		d.Busy = true
		data, next, err := src.Next()
		d.Busy = false
		msg := &Observer[T]{
			Source: next,
			Data:   data,
			Err:    err,
		}
		return msg
	}
}
func (d *Observer[T]) Update(msg tea.Msg) (cmd tea.Cmd, changed bool) {
	if d.Source != nil {
		return d.refresh(0), false
	}
	if msg, ok := msg.(*Observer[T]); ok {
		if msg.Err == io.EOF {
			d.Source = nil
			return nil, false
		}
		d.Source = msg.Source
		if msg.Err == nil {
			d.Data = msg.Data
		}
		d.Err = msg.Err
		return d.refresh(updateRateLimit), true
	}
	return nil, false
}
func (d *Observer[T]) Observe(msg tea.Msg, onChanged func(T, error) tea.Cmd) (cmd tea.Cmd) {
	cmd, changed := d.Update(msg)
	if changed {
		if onChanged != nil {
			c2 := onChanged(d.Data, d.Err)
			if cmd == nil {
				cmd = c2
			} else {
				cmd = tea.Batch(cmd, c2)
			}
		}
	}
	return cmd
}
func MakeObserver[T any](src Store[T]) Observer[T] {
	return Observer[T]{Source: src}
}

func MapToStableList[K cmp.Ordered, V any, E any](m map[K]V, cvt func(K, V) E) []E {
	keys := lo.Keys(m)
	slices.Sort(keys)
	items := make([]E, len(keys))
	for i, k := range keys {
		items[i] = cvt(k, m[k])
	}
	return items
}
