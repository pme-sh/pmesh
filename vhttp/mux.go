package vhttp

import (
	"net/http"
)

type route struct {
	Pattern Pattern
	Handler Handler
}

func (r *route) String() string {
	return r.Pattern.String() + " -> " + r.Handler.String()
}

type Mux struct {
	Routes []*route
}

func (mux *Mux) UsePattern(p Pattern, mw Handler) {
	mux.Routes = append(mux.Routes, &route{p, mw})
}
func (mux *Mux) Use(spattern string, mw Handler) error {
	pattern, err := NewPattern(spattern)
	if err != nil {
		return err
	}
	mux.UsePattern(pattern, mw)
	return nil
}
func (mux *Mux) Then(mw Handler) {
	mux.UsePattern(Pattern{}, mw)
}
func (mux *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	for _, route := range mux.Routes {
		if route.Pattern.Match(r.URL.Host, r.URL.Path) {
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
