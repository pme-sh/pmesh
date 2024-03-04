package session

import (
	"net/http"

	"get.pme.sh/pmesh/vhttp"
)

func init() {
	ApiRouter.HandleFunc("/nats/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		nats := RequestSession(r).Nats
		if nats == nil {
			vhttp.Error(w, r, http.StatusServiceUnavailable, "NATS not available")
			return
		}
		sv := nats.Server
		if sv == nil {
			vhttp.Error(w, r, http.StatusServiceUnavailable, "NATS server not available")
			return
		}
		r.URL.Path = "/" + r.PathValue("rest")
		sv.ServeHTTP(w, r)
	})
}
