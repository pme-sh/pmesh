package vhttp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/enats"
	"get.pme.sh/pmesh/ray"
	"get.pme.sh/pmesh/variant"
	"get.pme.sh/pmesh/xlog"
	"github.com/nats-io/nats.go"
)

type StateResolver interface {
	ResolveService(s string) Handler
	ResolveNats() *enats.Client
}
type stateResolver struct{}

func WithStateResolver(ctx context.Context, r StateResolver) context.Context {
	return context.WithValue(ctx, stateResolver{}, r)
}
func StateResolverFromContext(ctx context.Context) StateResolver {
	r, _ := ctx.Value(stateResolver{}).(StateResolver)
	return r
}
func ResolveServiceFromContext(ctx context.Context, s string) Handler {
	if r := StateResolverFromContext(ctx); r != nil {
		return r.ResolveService(s)
	}
	return nil
}
func ResolveNatsFromContext(ctx context.Context) *enats.Client {
	if r := StateResolverFromContext(ctx); r != nil {
		return r.ResolveNats()
	}
	return nil
}

type HandlePublish struct {
	topic  string
	beacon bool
}

func (h HandlePublish) String() string {
	return fmt.Sprintf("Publish(%s)", h.topic)
}
func (h *HandlePublish) UnmarshalText(text []byte) error {
	return h.UnmarshalInline(string(text))
}
func (h *HandlePublish) UnmarshalInline(text string) error {
	var ok bool
	if h.beacon {
		text, ok = strings.CutPrefix(text, "beacon ")
	} else {
		text, ok = strings.CutPrefix(text, "publish ")
	}
	if !ok {
		return variant.RejectMatch(h)
	}
	if strings.IndexByte(text, ' ') >= 0 {
		return variant.RejectMatch(h)
	}
	h.topic = enats.ToSubject(text)
	return nil
}
func (h HandlePublish) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	// Resolve NATS client
	cli := ResolveNatsFromContext(r.Context())
	if cli == nil {
		xlog.WarnC(r.Context()).Msg("NATS client not available")
		Error(w, r, http.StatusServiceUnavailable)
		return Done
	}

	// Read request body
	data, err := io.ReadAll(r.Body)
	if err != nil {
		if r.Context().Err() != nil {
			return Done
		}
		xlog.WarnC(r.Context()).Err(err).Msg("Failed to read request body")
		Error(w, r, http.StatusInternalServerError)
		return Done
	} else if len(data) == 0 {
		delete(r.Header, "Content-Length")
		delete(r.Header, "Content-Type")
	}

	// Create message
	msg := &nats.Msg{
		Subject: h.topic,
		Data:    data,
		Header:  nats.Header(r.Header),
	}
	msg.Header["Referer"] = []string{r.URL.String()}
	msg.Header["X-Forwarded-Method"] = []string{r.Method}

	// If beacon, publish and return
	if h.beacon {
		err := cli.PublishMsg(msg)
		if err != nil {
			xlog.WarnC(r.Context()).Str("topic", h.topic).Err(err).Msg("Failed to publish message")
			Error(w, r, StatusPublishError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return Done
	}

	// Otherwise, request message
	deadline, ok := r.Context().Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	res, err := cli.RequestMsg(msg, time.Until(deadline))
	if err != nil {
		xlog.WarnC(r.Context()).Str("topic", h.topic).Err(err).Msg("Failed to publish message")
		Error(w, r, StatusPublishError)
	} else {
		hout := w.Header()
		status := http.StatusOK
		for k, v := range res.Header {
			if k == "Status" && len(v) == 1 {
				if st, err := strconv.Atoi(v[0]); err == nil {
					status = st
					continue
				}
			}
			hout[k] = v
		}
		w.WriteHeader(status)
		w.Write(res.Data)
	}
	return Done
}

type HandleService struct {
	name string
	tag  string
}

func (h HandleService) String() string {
	return fmt.Sprintf("Service(%s)", h.name)
}
func (h *HandleService) UnmarshalText(text []byte) error {
	return h.UnmarshalInline(string(text))
}
func (h *HandleService) UnmarshalInline(text string) error {
	if strings.IndexByte(text, ' ') >= 0 {
		return variant.RejectMatch(h)
	}
	h.name = text
	h.tag = text + "." + ray.ToHostString(config.Get().Host)
	return nil
}
func (h HandleService) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	service := ResolveServiceFromContext(r.Context(), h.name)
	if service == nil {
		Error(w, r, http.StatusServiceUnavailable)
		return Done
	} else {
		w.Header()["X-Service"] = []string{h.tag}
		return service.ServeHTTP(w, r)
	}
}

func init() {
	Registry.Define("Service", func() any { return &HandleService{} })
	Registry.Define("Publish", func() any { return &HandlePublish{} })
	Registry.Define("Beacon", func() any { return &HandlePublish{beacon: true} })
}
