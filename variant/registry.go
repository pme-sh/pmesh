package variant

import (
	"fmt"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

/*
	"id:kind"
	"id:kind": {
		details
	}
	"id:kind arg1="arg2" arg3=[arg4]
*/

type InlineUnmarshaler interface {
	UnmarshalInline(text string) (err error)
}

type Factory = func(node *yaml.Node) (any, error)
type Registration struct {
	FromNode   func(node *yaml.Node) (any, error)
	FromString func(str string) (any, error)
	Instance   any
}

type Registry[I any] struct {
	Tags map[string]*Registration
}

var fmtErrRejectedMatch = "failed to decode as [%s]"

func RejectMatch(self any) error {
	return &yaml.TypeError{
		Errors: []string{
			fmt.Sprintf(
				fmtErrRejectedMatch,
				reflect.TypeOf(self).Elem().Name(),
			),
		},
	}
}

var fmtErrNoUnmarshaler = "[%s.%s] no match found for: %s"

func noMatchError[I any](node *yaml.Node) error {
	return fmt.Errorf(
		fmtErrNoUnmarshaler,
		reflect.TypeOf((*I)(nil)).Elem().Name(),
		node.Tag,
		node.Value,
	)
}
func combineErrs(w error, a ...error) (err error) {
	err = w
	for _, e := range a {
		if e == nil {
			continue
		}
		if err == nil {
			err = e
		} else if _, ok := err.(*yaml.TypeError); ok {
			if _, ok2 := e.(*yaml.TypeError); !ok2 {
				err = e
			} else {
				err.(*yaml.TypeError).Errors = append(err.(*yaml.TypeError).Errors, e.(*yaml.TypeError).Errors...)
			}
		}
	}
	return
}

func (r *Registry[I]) Unmarshal(node *yaml.Node) (res I, err error) {
	if node.Tag != "" && !strings.HasPrefix(node.Tag, "!!") {
		if reg, ok := r.Tags[node.Tag[1:]]; ok {
			// Adjust the tag to the correct type
			node.Tag = ""
			node.Tag = node.ShortTag()

			// Call the unmarshaler
			var result any
			if result, err = reg.FromNode(node); err == nil {
				res = result.(I)
			}
			return
		}
		err = noMatchError[I](node)
		return
	}

	if node.Kind == yaml.ScalarNode {
		var str string
		if serr := node.Decode(&str); serr == nil {

			if !strings.Contains(str, " ") {
				// Prioritize the tag with the same name
				if reg, ok := r.Tags[str]; ok {
					if sfc := reg.FromString; sfc != nil {
						result, serr := sfc(str)
						if serr == nil {
							res = result.(I)
							return
						}
					}
				}
			}

			for _, reg := range r.Tags {
				if sfc := reg.FromString; sfc != nil {
					result, serr := sfc(str)
					if serr == nil {
						res = result.(I)
						return
					}
				}
			}
			err = fmt.Errorf("no inline match found for: %s", str)
		}
	}

	// Last resort
	if node.Kind != yaml.MappingNode {
		for _, reg := range r.Tags {
			if result, e := reg.FromNode(node); e == nil {
				return result.(I), nil
			} else {
				err = combineErrs(err, e)
			}
		}
	}
	if err == nil {
		err = noMatchError[I](node)
	}
	return
}

func (r *Registry[I]) Define(tag string, defaults func() any) {
	defaultValue := defaults()
	if _, ok := defaultValue.(I); !ok {
		panic("defaults must implement the interface")
	}
	reg := &Registration{
		Instance: defaultValue,
	}
	r.Tags[tag] = reg

	reg.FromNode = func(node *yaml.Node) (res any, err error) {
		result := defaults()
		if node.Kind == yaml.ScalarNode {
			// If the scalar is null, return defaults (empty map)
			if node.Tag == "!!null" {
				return result, nil
			} else if node.Tag == "!!str" {
				// Try to unmarshal as a string
				if iu, ok := result.(InlineUnmarshaler); ok {
					var text string
					if err := node.Decode(&text); err != nil {
						return nil, err
					}
					err = iu.UnmarshalInline(text)
					return result, err
				}
			}
			return nil, RejectMatch(result)
		}
		if err := node.Decode(result); err != nil {
			return nil, err
		}
		return result, nil
	}

	if _, ok := defaultValue.(InlineUnmarshaler); ok {
		reg.FromString = func(str string) (res any, err error) {
			result := defaults()
			if iu, ok := result.(InlineUnmarshaler); ok {
				err = iu.UnmarshalInline(str)
				return result, err
			}
			return nil, RejectMatch(result)
		}
	}
}

func NewRegistry[IFace any]() *Registry[IFace] {
	reg := &Registry[IFace]{
		Tags: make(map[string]*Registration),
	}
	return reg
}
