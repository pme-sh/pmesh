package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/pme-sh/pmesh/health"
	"github.com/pme-sh/pmesh/lb"
	"github.com/pme-sh/pmesh/util"
	"github.com/pme-sh/pmesh/vhttp"
	"github.com/pme-sh/pmesh/xlog"
)

type ProxyService struct {
	Options
	Monitor      health.Monitor    `yaml:"monitor,omitempty"`
	LoadBalancer lb.LoadBalancer   `yaml:"lb,omitempty"`
	Upstreams    util.Some[string] `yaml:"upstreams,omitempty"`
}

func (px *ProxyService) UnmarshalInline(text string) error {
	for _, line := range strings.Split(text, ",") {
		px.Upstreams = append(px.Upstreams, strings.TrimSpace(line))
	}
	return nil
}

func init() {
	Registry.Define("Proxy", func() any { return &ProxyService{} })
}

// Implement service.Service
func (px *ProxyService) String() string {
	return fmt.Sprintf("Proxy{Upstreams: %v}", px.Upstreams)
}
func (px *ProxyService) Prepare(opt Options) error {
	px.Options = opt
	if len(px.Upstreams) == 0 {
		return fmt.Errorf("no upstreams defined")
	}

	px.LoadBalancer.SetLogger(xlog.NewDomain(px.Name + ".lb"))
	return nil
}
func (px *ProxyService) Start(c context.Context, invaliate bool) (Instance, error) {
	for _, address := range px.Upstreams {
		upstream := lb.NewHttpUpstream(address)
		px.LoadBalancer.AddUpstream(upstream)
		ml := px.Options.Logger.With().Str("upstream", address).Logger()
		px.Monitor.Observe(c, &ml, address, upstream)
	}
	return px, nil
}

// Implement service.Instance and service.InstanceLB
func (px *ProxyService) ServeHTTP(w http.ResponseWriter, r *http.Request) vhttp.Result {
	px.LoadBalancer.ServeHTTP(w, r)
	return vhttp.Done
}
func (px *ProxyService) Stop(context.Context) {
	px.LoadBalancer.ClearUpstreams()
}
func (px *ProxyService) GetLoadBalancer() *lb.LoadBalancer {
	return &px.LoadBalancer
}
