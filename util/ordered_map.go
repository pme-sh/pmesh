package util

import (
	"errors"

	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

type OrderedMap[K comparable, V any] []lo.Tuple2[K, V]

func (m *OrderedMap[K, V]) Set(key K, value V) {
	for i, kv := range *m {
		if kv.A == key {
			(*m)[i].B = value
			return
		}
	}
	*m = append(*m, lo.Tuple2[K, V]{A: key, B: value})
}
func (m OrderedMap[K, V]) Get(key K) (v V, ok bool) {
	for _, kv := range m {
		if kv.A == key {
			return kv.B, true
		}
	}
	return
}
func (m OrderedMap[K, V]) ForEach(fn func(k K, v V)) {
	for _, kv := range m {
		fn(kv.A, kv.B)
	}
}
func (m *OrderedMap[K, V]) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return errors.New("expected a map")
	}
	for i := 0; i < len(node.Content); i += 2 {
		var kv lo.Tuple2[K, V]
		if e := node.Content[i].Decode(&kv.A); e != nil {
			return e
		}
		if e := node.Content[i+1].Decode(&kv.B); e != nil {
			return e
		}
		*m = append(*m, kv)
	}
	return nil
}
func (m OrderedMap[K, V]) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, kv := range m {
		var k, v yaml.Node
		if e := k.Encode(kv.A); e != nil {
			return nil, e
		}
		if e := v.Encode(kv.B); e != nil {
			return nil, e
		}
		node.Content = append(node.Content, &k, &v)
	}
	return node, nil
}
