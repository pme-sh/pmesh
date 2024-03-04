package health

import (
	"context"

	"get.pme.sh/pmesh/variant"

	"gopkg.in/yaml.v3"
)

type checker interface {
	Perform(ctx context.Context, addr string) error
}

type Checker struct {
	checker
}

func NewChecker(c checker) Checker {
	return Checker{c}
}

var Registry = variant.NewRegistry[checker]()

func (t *Checker) UnmarshalYAML(node *yaml.Node) (e error) {
	t.checker, e = Registry.Unmarshal(node)
	return
}
