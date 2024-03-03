package util

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

type Some[T any] []T

func Many[T any](a ...T) Some[T] {
	return a
}
func One[T any](a T) Some[T] {
	return []T{a}
}

func (a Some[T]) IsZero() bool {
	return len(a) == 0
}
func (a Some[T]) ForEach(f func(*T)) {
	for i := range a {
		f(&a[i])
	}
}
func (a Some[T]) Filter(f func(*T) bool) []T {
	var res []T
	for i := range a {
		if f(&a[i]) {
			res = append(res, a[i])
		}
	}
	return res
}
func (a Some[T]) Elements() []T {
	return a
}
func (a *Some[T]) String() string {
	return fmt.Sprintf("%v", *a)
}
func (a Some[T]) MarshalYAML() (any, error) {
	return []T(a), nil
}
func (a *Some[T]) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		var res T
		if err := node.Decode(&res); err != nil {
			return err
		}
		*a = []T{res}
	} else {
		var res []T
		if err := node.Decode(&res); err != nil {
			return err
		}
		*a = res
	}
	return nil
}
func (a Some[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal([]T(a))
}
func (a *Some[T]) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[0] != '[' {
		var res T
		if err := json.Unmarshal(data, &res); err != nil {
			return err
		}
		*a = []T{res}
	} else {
		var res []T
		if err := json.Unmarshal(data, &res); err != nil {
			return err
		}
		*a = res
	}
	return nil
}
