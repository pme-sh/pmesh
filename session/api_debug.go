package session

import (
	"fmt"
	"net/http"
	"net/http/pprof"

	"get.pme.sh/pmesh/config"
)

func init() {
	ApiRouter.HandleFunc("/debug/pprof/", pprof.Index)
	ApiRouter.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	ApiRouter.HandleFunc("/debug/pprof/profile", pprof.Profile)
	ApiRouter.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	ApiRouter.HandleFunc("/debug/pprof/trace", pprof.Trace)
	ApiRouter.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	ApiRouter.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	ApiRouter.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	ApiRouter.Handle("/debug/pprof/block", pprof.Handler("block"))
	ApiRouter.HandleFunc("/debug/headers", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s /debug/headers\n", r.Method)
		for k, v := range r.Header {
			fmt.Fprintf(w, "%s: %v\n", k, v)
		}
	})
	Match("/ping", func(session *Session, r *http.Request, p struct{}) (res string, err error) {
		res = config.Get().Host
		return
	})
}
