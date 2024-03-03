package xlog

import (
	"net/http"
)

type EventEnhancer interface {
	MarshalZerologObject(e *Event)
}

type httpEnhancer struct {
	*http.Request
}

func (h httpEnhancer) getHeader(hd string) string {
	if h.Header == nil {
		return ""
	}
	if v := h.Header[hd]; len(v) > 0 {
		return v[0]
	}
	return ""
}
func (h httpEnhancer) putHeader(e *Event, hdr string, field string) {
	if v := h.getHeader(hdr); v != "" {
		e.Str(field, v)
	}
}

var j1 = []byte(`1`)

func (h httpEnhancer) MarshalZerologObject(e *Event) {
	if !e.Enabled() {
		return
	}
	e.Str("method", h.Method)
	e.Stringer("url", h.URL)
	h.putHeader(e, "P-Asn", "asn")
	h.putHeader(e, "X-Ray", "ray")
	e.Str("adr", h.RemoteAddr)
	if h.ProtoMajor == 2 {
		e.RawJSON("h2", j1)
	}

	// Add context if not already present
	if e.GetCtx().Value(http.ServerContextKey) == nil {
		e.Ctx(h.Context())
	}
}

func EnhanceRequest(r *http.Request) EventEnhancer {
	return httpEnhancer{r}
}
