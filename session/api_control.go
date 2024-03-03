package session

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pme-sh/pmesh/enats"
	"github.com/pme-sh/pmesh/vhttp"
	"github.com/pme-sh/pmesh/xpost"

	"github.com/nats-io/nats.go/jetstream"
)

func init() {
	ApiRouter.HandleFunc("/hop/{peer}/{host}/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		peerid, host, rest := r.PathValue("peer"), r.PathValue("host"), r.PathValue("rest")
		peerid = strings.ToLower(peerid)

		session := RequestSession(r)
		peer := session.Peerlist.Find(peerid)
		if peer == nil {
			writeOutput(r, w, nil, fmt.Errorf("peer not found"))
			return
		}
		context, cancel := context.WithCancel(r.Context())
		defer cancel()
		req := r.Clone(context)
		req.RequestURI = ""
		req.URL.Path = "/" + rest
		req.URL.Host = host

		if peer.Me {
			vhttp.GetServerFromContext(req.Context()).ServeHTTP(w, req)
		} else {
			res, err := peer.SendRequest(req)
			if err != nil {
				writeOutput(r, w, nil, err)
				return
			}
			defer res.Body.Close()
			for k, v := range res.Header {
				w.Header()[k] = v
			}
			w.WriteHeader(res.StatusCode)
			io.Copy(w, res.Body)
		}
	})

	Match("/shutdown", func(session *Session, r *http.Request, p struct{}) (_ any, err error) {
		go func() {
			session.Shutdown(context.Background())
			session.Close()
		}()
		return
	})
	Match("/peers", func(session *Session, r *http.Request, p struct{}) (res []xpost.Peer, _ error) {
		res = session.Peerlist.List(false)
		return
	})
	Match("/peers/alive", func(session *Session, r *http.Request, p struct{}) (res []xpost.Peer, err error) {
		res = session.Peerlist.List(true)
		return
	})
	Match("/publish/{topic}", func(session *Session, r *http.Request, p json.RawMessage) (ack jetstream.PubAck, err error) {
		subject := enats.ToPublisherSubject(r.PathValue("topic"))
		a, e := session.Nats.Jet.Publish(r.Context(), subject, p)
		if e != nil {
			err = e
			return
		}
		return *a, nil
	})
	MatchLocked("/reload", func(session *Session, r *http.Request, p ServiceInvalidate) (_ any, err error) {
		err = session.ReloadLocked(p.Invalidate)
		return
	})
}
