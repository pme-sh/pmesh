package vhttp

import (
	"gopkg.in/yaml.v3"
)

type HandleMux struct {
	Mux
}

func (h *HandleMux) String() string {
	res := "Mux:\n"
	for _, route := range h.Mux.Routes {
		res += "  " + route.String() + "\n"
	}
	return res
}

type muxRoute struct {
	Pattern Pattern
	Handler Subhandler
}

func (m *muxRoute) UnmarshalYAML(node *yaml.Node) (e error) {
	// Either map[string]Subhandler or Subhandler
	if node.Kind == yaml.MappingNode && len(node.Content) == 2 {
		if e = node.Content[0].Decode(&m.Pattern); e == nil {
			e = node.Content[1].Decode(&m.Handler)
		}
	} else {
		m.Pattern = Pattern{}
		e = node.Decode(&m.Handler)
	}
	return
}

func (h *HandleMux) UnmarshalYAML(node *yaml.Node) (e error) {
	var data []muxRoute
	if e = node.Decode(&data); e != nil {
		return
	}
	for _, route := range data {
		h.Mux.UsePattern(route.Pattern, route.Handler)
	}
	return nil
}

func init() {
	Registry.Define("Mux", func() any { return &HandleMux{} })
}
