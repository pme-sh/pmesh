package service

import (
	"context"

	"github.com/pme-sh/pmesh/lb"
	"github.com/pme-sh/pmesh/variant"
	"github.com/pme-sh/pmesh/vhttp"
	"github.com/pme-sh/pmesh/xlog"

	"gopkg.in/yaml.v3"
)

type Options struct {
	Name        string       `yaml:"-"`
	ServiceRoot string       `yaml:"-"`
	Logger      *xlog.Logger `yaml:"-"`
}

type Instance interface {
	// Handles requests
	vhttp.Handler
	// Gracefully stops the instance
	Stop(c context.Context)
}
type InstanceLB interface {
	GetLoadBalancer() *lb.LoadBalancer
}
type InstanceProc interface {
	GetProcessTrees() []ProcessTree
}

type service interface {
	// Prepare the service for use, called after unmarshalling
	Prepare(opt Options) error

	// Starts the service
	Start(c context.Context, invaliate bool) (i Instance, e error)

	// String representation of the service
	String() string
}

type Service struct {
	service
}

var Registry = variant.NewRegistry[service]()

func (t *Service) UnmarshalYAML(node *yaml.Node) (e error) {
	t.service, e = Registry.Unmarshal(node)
	return
}
