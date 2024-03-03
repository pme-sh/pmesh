package vhttp

import (
	"fmt"
	"net/http"

	"gopkg.in/yaml.v3"
)

type SwitchVariable interface {
	Name() string
	Value(r *http.Request) string
}
type switchVariableFunc struct {
	name string
	f    func(r *http.Request) string
}

func (v *switchVariableFunc) Name() string {
	return v.name
}
func (v *switchVariableFunc) Value(r *http.Request) string {
	return v.f(r)
}

func RegisterSwitchVariable(v SwitchVariable) {
	Registry.Define(fmt.Sprintf("Switch-%s", v.Name()), func() any { return &HandleSwitch{Variable: v} })
}

func RegisterSwitchVariableFunc(name string, f func(r *http.Request) string) SwitchVariable {
	v := &switchVariableFunc{name, f}
	RegisterSwitchVariable(v)
	return v
}

type HandleSwitch struct {
	Mux
	Variable SwitchVariable
}

func (h *HandleSwitch) String() string {
	res := fmt.Sprintf("Switch-%s:\n", h.Variable.Name())
	for _, route := range h.Mux.Routes {
		res += "  " + route.String() + "\n"
	}
	return res
}

func (h *HandleSwitch) UnmarshalYAML(node *yaml.Node) (e error) {
	var data []muxRoute
	if e = node.Decode(&data); e != nil {
		return
	}
	for _, route := range data {
		h.Mux.UsePattern(route.Pattern, route.Handler)
	}
	return nil
}
func (h *HandleSwitch) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	value := h.Variable.Value(r)
	for _, route := range h.Routes {
		if route.Pattern.Match(value, "") {
			switch route.Handler.ServeHTTP(w, r) {
			case Done:
				return Done
			case Drop:
				return Continue
			}
		}
	}
	return Continue
}

func init() {
	RegisterSwitchVariable(&switchVariableFunc{"Host", func(r *http.Request) string { return r.Host }})
	RegisterSwitchVariable(&switchVariableFunc{"Path", func(r *http.Request) string { return r.URL.Path }})
	RegisterSwitchVariable(&switchVariableFunc{"Method", func(r *http.Request) string { return r.Method }})
	RegisterSwitchVariable(&switchVariableFunc{"Proto", func(r *http.Request) string { return r.Proto }})
	RegisterSwitchVariable(&switchVariableFunc{"UserAgent", func(r *http.Request) string { return r.UserAgent() }})
	RegisterSwitchVariable(&switchVariableFunc{"Referer", func(r *http.Request) string { return r.Referer() }})
}
