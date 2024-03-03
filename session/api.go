package session

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pme-sh/pmesh/vhttp"
	"github.com/pme-sh/pmesh/xlog"

	"encoding/json"

	"github.com/pme-sh/pmesh/pmtp"

	"github.com/gorilla/schema"
)

func parseInput(into any, w http.ResponseWriter, req *http.Request) (err error) {
	// If we don't take any input, we're done.
	if _, ok := into.(*struct{}); ok {
		return nil
	}
	if p, ok := into.(*http.ResponseWriter); ok {
		*p = w
		return nil
	}

	// Mix URL query parameters.
	implicit := req.URL.Query()
	if len(implicit) != 0 {
		schema.NewDecoder().Decode(into, implicit)
	}

	// If it's not a POST, PUT, or PATCH, we're done.
	if req.Method != "POST" && req.Method != "PUT" && req.Method != "PATCH" {
		return nil
	}

	// If there is a post form, read that and finish.
	if len(req.PostForm) != 0 {
		dec := schema.NewDecoder()
		dec.IgnoreUnknownKeys(false)
		return dec.Decode(into, req.PostForm)
	}

	// If there is a body, assume it's JSON and unmashal.
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	err = dec.Decode(into)

	// If error is EOF indicating there was no body, ignore it, our fault for assuming.
	if err == io.EOF {
		err = nil
	}
	return
}

func writeOutput(req *http.Request, w http.ResponseWriter, result any, e error) {
	if e == http.ErrAbortHandler {
		return
	}

	req.Body.Close()
	w.Header()["Content-Type"] = []string{"application/json; charset=utf-8"}

	var output any
	if e != nil {
		w.WriteHeader(http.StatusBadRequest)
		if marshaller, ok := e.(json.Marshaler); ok {
			res, err := marshaller.MarshalJSON()
			if err == nil && len(res) != 0 {
				output = res
			}
		}

		if output == nil {
			msg := e.Error()
			if msg == "" {
				msg = "error"
			}
			output = msg
		}
		output = map[string]any{"error": output}
	} else {
		if result == nil {
			w.Write([]byte(`{}`))
			return
		}
		output = result
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if req.URL.Query().Has("pretty") {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(output); err != nil {
		xlog.WarnC(req.Context()).Err(err).Msg("error encoding result")
		vhttp.Error(w, req, http.StatusInternalServerError)
	}
}

// Handler: func(arg T, req *http.Request) (result U, err error)
type TypedHandler[T any, U any] struct {
	Callback func(*Session, *http.Request, T) (U, error)
}

func (h TypedHandler[T, U]) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var arg T
	if err := parseInput(&arg, w, req); err != nil {
		writeOutput(req, w, nil, err)
		return
	}
	result, err := h.Callback(RequestSession(req), req, arg)
	writeOutput(req, w, result, err)
}

type LockedHandler struct {
	Handler http.Handler
}

func (h LockedHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	session := RequestSession(req)
	if !session.TryLockContext(req.Context()) {
		writeOutput(req, w, nil, fmt.Errorf("failed to lock session in time"))
		return
	}
	defer session.Unlock()
	h.Handler.ServeHTTP(w, req)
}

var ApiRouter = http.NewServeMux()

// Forwards to serve HTTP
func ServeRPC(originalReq *http.Request, method string, body json.RawMessage) (out json.RawMessage, err error) {
	context, cancel := context.WithCancel(originalReq.Context())
	defer cancel()
	req := originalReq.Clone(context)
	req.Body = io.NopCloser(bytes.NewReader(body))
	if strings.HasPrefix(method, "/") {
		req.Method = "POST"
		req.URL.Path = method
	} else {
		req.Method, req.URL.Path, _ = strings.Cut(method, " ")
	}
	req.RequestURI = ""

	buf := vhttp.NewBufferedResponse(nil)
	ApiRouter.ServeHTTP(buf, req)
	if buf.Status != http.StatusOK {
		if buf.Body.Len() == 0 {
			err = fmt.Errorf("error %d", buf.Status)
		} else {
			err = fmt.Errorf("error %d: %s", buf.Status, buf.Body.String())
		}
	} else {
		out = buf.Body.Bytes()
	}
	return
}

func init() {
	sv := pmtp.MakeRPCServer(func(conn net.Conn, code pmtp.Code, r *http.Request) {
		code.Serve(conn, pmtp.ServerFunc(func(method string, body json.RawMessage) (any, error) {
			return ServeRPC(r, method, body)
		}))
	})
	ApiRouter.HandleFunc("GET /connect", func(w http.ResponseWriter, r *http.Request) {
		sv.Upgrade(w, r, r)
	})
}

const ApiRequestMaxDuration = time.Minute

func RequestSession(r *http.Request) *Session {
	return vhttp.StateResolverFromContext(r.Context()).(*Session)
}
func Match[T any, U any](path string, cb func(*Session, *http.Request, T) (U, error)) {
	ApiRouter.Handle(path, TypedHandler[T, U]{cb})
}
func MatchLocked[T any, U any](path string, cb func(*Session, *http.Request, T) (U, error)) {
	ApiRouter.Handle(path, LockedHandler{TypedHandler[T, U]{cb}})
}

type apiHandler struct{}

func (h apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) vhttp.Result {
	_, pattern := ApiRouter.Handler(r) // Waste of time, but it's the only way to avoid 404s.
	if pattern == "" {
		vhttp.Error(w, r, http.StatusNotFound)
	} else {
		ApiRouter.ServeHTTP(w, r)
	}
	return vhttp.Done
}
func (h apiHandler) String() string {
	return "API Gateway"
}
func CreateAPIHost(session *Session) *vhttp.VirtualHost {
	vh := vhttp.NewVirtualHost(vhttp.VirtualHostOptions{
		Hostnames: []string{"pm3"},
	})
	vh.Mux.Then(vhttp.InternalHandler{
		Inner:     vhttp.Subhandler{Handler: apiHandler{}},
		Protected: true,
	})
	return vh
}
