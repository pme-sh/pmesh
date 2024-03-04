package vhttp

import (
	"net/http"

	"get.pme.sh/pmesh/variant"

	"gopkg.in/yaml.v3"
)

type Result uint8

const (
	Done     Result = iota // Finished handling the request, return all the way up.
	Continue               // Continue to the next handler.
	Drop                   // Drop from this mux, but continue to the next.
)

type Handler interface {
	ServeHTTP(w http.ResponseWriter, r *http.Request) Result
	String() string
}
type Subhandler struct {
	Handler
}

var Registry = variant.NewRegistry[Handler]()

func (t *Subhandler) UnmarshalYAML(node *yaml.Node) (e error) {
	t.Handler, e = Registry.Unmarshal(node)
	return
}
