package util

import (
	"fmt"
	"strings"

	"github.com/samber/lo"
)

type Enum[T comparable] struct {
	Names     map[T]string
	Values    map[string]T
	errFormat string
}

func NewEnum[T comparable](names map[T]string) Enum[T] {
	values := make(map[string]T, len(names))
	for k, v := range names {
		values[v] = k
	}
	options := lo.Keys(values)
	return Enum[T]{
		Names:     names,
		Values:    values,
		errFormat: "invalid value %q, expected one of: " + strings.Join(options, ", "),
	}
}

func (e Enum[T]) ToString(value T) string {
	return e.Names[value]
}
func (e Enum[T]) ToValue(name string) (T, bool) {
	value, ok := e.Values[name]
	if name == "" {
		var zero T
		value, ok = zero, true
	}
	return value, ok
}

func (e Enum[T]) MarshalText(value T) (text []byte, err error) {
	return []byte(e.ToString(value)), nil
}
func (e Enum[T]) UnmarshalText(into *T, text []byte) error {
	val, ok := e.ToValue(string(text))
	if !ok {
		return fmt.Errorf(e.errFormat, string(text))
	}
	*into = val
	return nil
}
