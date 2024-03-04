package vhttp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/enats"
	"get.pme.sh/pmesh/ray"
	"get.pme.sh/pmesh/variant"
	"get.pme.sh/pmesh/xlog"
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
	topic      string
	jet        bool
	typePrefix string // "publish " or "beacon "
}

func (h HandlePublish) String() string {
	if h.jet {
		return fmt.Sprintf("JPublish(%s)", h.topic)
	}
	return fmt.Sprintf("Publish(%s)", h.topic)
}
func (h *HandlePublish) UnmarshalText(text []byte) error {
	return h.UnmarshalInline(string(text))
}
func (h *HandlePublish) UnmarshalInline(text string) error {
	text, ok := strings.CutPrefix(text, h.typePrefix)
	if !ok {
		return variant.RejectMatch(h)
	}
	if strings.IndexByte(text, ' ') >= 0 {
		return variant.RejectMatch(h)
	}
	if strings.HasPrefix(text, "?") {
		h.jet = false
		h.topic = text[1:]
	} else {
		h.jet = true
		h.topic = enats.ToPublisherSubject(text)
	}
	return nil
}
func (h HandlePublish) publish(ctx context.Context, cli *enats.Client, reqdata []byte) (err error) {
	if h.jet {
		_, err = cli.Jet.Publish(ctx, h.topic, reqdata)
	} else {
		err = cli.Publish(h.topic, reqdata)
	}
	if err != nil {
		xlog.WarnC(ctx).Str("topic", h.topic).Err(err).Msg("Failed to publish message")
	}
	return
}
func (h HandlePublish) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	cli := ResolveNatsFromContext(r.Context())
	if cli == nil {
		xlog.WarnC(r.Context()).Msg("NATS client not available")
		Error(w, r, http.StatusServiceUnavailable)
		return Done
	}

	buffer := bytes.NewBuffer(nil)
	err := r.Write(buffer)
	if err != nil {
		if r.Context().Err() != nil {
			return Done
		}
		xlog.WarnC(r.Context()).Err(err).Msg("Failed to read request body")
		Error(w, r, http.StatusInternalServerError)
		return Done
	}

	if h.typePrefix == "beacon " {
		go h.publish(context.Background(), cli, buffer.Bytes())
		w.WriteHeader(http.StatusAccepted)
	} else {
		if err := h.publish(r.Context(), cli, buffer.Bytes()); err != nil {
			Error(w, r, StatusPublishError)
		} else {
			w.WriteHeader(http.StatusOK)
		}
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
	Registry.Define("Publish", func() any { return &HandlePublish{typePrefix: "publish "} })
	Registry.Define("Beacon", func() any { return &HandlePublish{typePrefix: "beacon "} })
}
