package session

import (
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/pme-sh/pmesh/lb"
	"github.com/pme-sh/pmesh/service"
	"github.com/pme-sh/pmesh/snowflake"
)

type ServiceHealth struct {
	Status  string `json:"status"`        // Status
	Healthy int    `json:"healthy"`       // Number of healthy instances
	Total   int    `json:"total"`         // Total number of instances
	Err     string `json:"err,omitempty"` // Error message
}
type ServiceMetrics struct {
	ID        snowflake.ID              `json:"id"`
	Type      string                    `json:"type"`
	Server    lb.LoadBalancerMetrics    `json:"server"`
	Processes []service.ProcTreeMetrics `json:"processes"`
}
type ServiceInfo struct {
	ServiceMetrics
	ServiceHealth
}

type ServiceEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ServiceList struct {
	Services []ServiceEntry `json:"services"`
}

func (h *ServiceHealth) Fill(sv *ServiceState) {
	if sv == nil {
		h.Status = "unknown"
		return
	}
	if err := sv.Err(); err != nil {
		h.Status = "Down"
		h.Err = err.Error()
		return
	}
	if l, ok := sv.GetLoadBalancer(); ok && l != nil {
		upstreams := l.Upstreams()
		h.Total = len(upstreams)
		for _, u := range upstreams {
			if u.Healthy.Load() {
				h.Healthy++
			}
		}
		if h.Healthy == 0 {
			h.Status = "Down"
		} else {
			h.Status = "OK"
		}
	} else {
		h.Status = "OK"
		h.Healthy = 1
	}
}
func (m *ServiceMetrics) Fill(sv *ServiceState) {
	if sv == nil {
		m.ID = 0
		return
	}
	m.ID = sv.ID

	if sv.Instance != nil {
		ty := reflect.TypeOf(sv.Instance)
		if ty.Kind() == reflect.Ptr {
			ty = ty.Elem()
		}
		m.Type = strings.ToLower(ty.Name())
		m.Type = strings.TrimSuffix(m.Type, "service")
		m.Type = strings.TrimSuffix(m.Type, "server")
	} else {
		m.Type = "-"
	}

	if trees, ok := sv.GetProcessTrees(); ok {
		m.Processes = make([]service.ProcTreeMetrics, len(trees))
		for i, tree := range trees {
			m.Processes[i] = tree.Metrics()
		}
	}
	if l, ok := sv.GetLoadBalancer(); ok && l != nil {
		m.Server = l.Metrics()
	}
}
func (m *ServiceInfo) Fill(sv *ServiceState) {
	m.ServiceMetrics.Fill(sv)
	m.ServiceHealth.Fill(sv)
}

func registerServiceView(name string, view func(*ServiceState) any) {
	Match("/service/"+name+"/{svc}", func(session *Session, r *http.Request, _ struct{}) (h any, _ error) {
		sv, _ := session.ServiceMap.Load(r.PathValue("svc"))
		h = view(sv)
		return
	})
	Match("/service/"+name, func(session *Session, r *http.Request, _ struct{}) (h map[string]any, _ error) {
		h = make(map[string]any)
		session.ServiceMap.Range(func(k string, v *ServiceState) bool {
			h[k] = view(v)
			return true
		})
		return
	})
}

type ServiceCommandResult struct {
	Count int `json:"count"`
}
type ServiceInvalidate struct {
	Invalidate bool `json:"invalidate"`
}

func init() {
	registerServiceView("health", func(sv *ServiceState) any {
		var h ServiceHealth
		h.Fill(sv)
		return h
	})
	registerServiceView("metrics", func(sv *ServiceState) any {
		var m ServiceMetrics
		m.Fill(sv)
		return m
	})
	registerServiceView("info", func(sv *ServiceState) any {
		var m ServiceInfo
		m.Fill(sv)
		return m
	})
	Match("/service", func(session *Session, r *http.Request, _ struct{}) (res map[string]snowflake.ID, _ error) {
		res = make(map[string]snowflake.ID)
		session.ServiceMap.Range(func(_ string, v *ServiceState) bool {
			res[v.name] = v.ID
			return true
		})
		return
	})

	Match("/service/restart/{svc}", func(session *Session, r *http.Request, p ServiceInvalidate) (res ServiceCommandResult, err error) {
		svcn := r.PathValue("svc")
		res.Count = session.RestartService(&svcn, p.Invalidate)
		if res.Count == 0 {
			err = errors.New("service not found")
		}
		return
	})
	Match("/service/restart", func(session *Session, r *http.Request, p ServiceInvalidate) (res ServiceCommandResult, _ error) {
		res.Count = session.RestartService(nil, p.Invalidate)
		return
	})
	Match("/service/stop/{svc}", func(session *Session, r *http.Request, p struct{}) (res ServiceCommandResult, err error) {
		svcn := r.PathValue("svc")
		res.Count = session.StopService(&svcn)
		if res.Count == 0 {
			err = errors.New("service not found")
		}
		return
	})
	Match("/service/stop", func(session *Session, r *http.Request, _ struct{}) (res ServiceCommandResult, _ error) {
		res.Count = session.StopService(nil)
		return
	})
}
