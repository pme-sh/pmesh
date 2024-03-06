package vhttp

import (
	"fmt"
	"net/http"
	"time"

	"get.pme.sh/pmesh/variant"

	"gopkg.in/yaml.v3"
)

type InternalHandler struct {
	Inner Subhandler
}

func (i InternalHandler) String() string {
	if i.Inner.Handler != nil {
		return fmt.Sprintf("Internal(%s)", i.Inner.Handler)
	} else {
		return "Internal"
	}
}
func (i *InternalHandler) UnmarshalText(text []byte) error {
	if string(text) != "internal" {
		return variant.RejectMatch(i)
	}
	return nil
}
func (i *InternalHandler) UnmarshalInline(text string) error {
	return i.UnmarshalText([]byte(text))
}
func (i *InternalHandler) UnmarshalYAML(node *yaml.Node) (err error) {
	if node.Kind != yaml.ScalarNode {
		var tmp struct {
			Inner *Subhandler `yaml:"inner"`
		}
		if err = node.Decode(&tmp); err != nil {
			return
		}
		if tmp.Inner != nil {
			i.Inner = *tmp.Inner
		} else {
			i.Inner = Subhandler{}
		}
		return
	} else {
		var text string
		if err = node.Decode(&text); err != nil {
			return
		}
		return i.UnmarshalInline(text)
	}
}

func (i InternalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	if r.Header.Get("P-Internal") != "1" {
		Error(w, r, http.StatusForbidden)
		return Done
	}

	// Remove write deadline.
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{})

	// Serve the request.
	if i.Inner.Handler != nil {
		return i.Inner.ServeHTTP(w, r)
	}
	return Continue
}

func init() {
	Registry.Define("internal", func() any { return &InternalHandler{} })
}
