package lb

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/pme-sh/pmesh/netx"
	"github.com/pme-sh/pmesh/util"
)

type Upstream struct {
	Address      string
	Healthy      atomic.Bool
	LoadFactor   atomic.Int32
	ReverseProxy httputil.ReverseProxy

	// Metrics
	RequestCount     atomic.Uint32
	ErrorCount       atomic.Uint32
	ServerErrorCount atomic.Uint32
	ClientErrorCount atomic.Uint32
}

func (u *Upstream) String() string {
	return u.Address
}

type UpstreamMetrics struct {
	Address          string `json:"address,omitempty"`
	Healthy          bool   `json:"healthy,omitempty"`
	LoadFactor       int32  `json:"load_factor,omitempty"`
	RequestCount     uint32 `json:"request_count,omitempty"`
	ErrorCount       uint32 `json:"error_count,omitempty"`
	ServerErrorCount uint32 `json:"server_error_count,omitempty"`
	ClientErrorCount uint32 `json:"client_error_count,omitempty"`
}

func (u *Upstream) Metrics() UpstreamMetrics {
	return UpstreamMetrics{
		Address:          u.Address,
		Healthy:          u.Healthy.Load(),
		LoadFactor:       u.LoadFactor.Load(),
		RequestCount:     u.RequestCount.Load(),
		ErrorCount:       u.ErrorCount.Load(),
		ServerErrorCount: u.ServerErrorCount.Load(),
		ClientErrorCount: u.ClientErrorCount.Load(),
	}
}

func (u *Upstream) SetHealthy(healthy bool) {
	u.Healthy.Store(healthy)
}

type SuppressedHttpError struct {
	http.Handler
}

func (err SuppressedHttpError) Error() string {
	return "suppressed http error"
}

type bufferType [32 * 1024]byte
type bufferPool struct {
	sync.Pool
}

func (b *bufferPool) Get() []byte {
	ptr := b.Pool.Get()
	return (*bufferType)(ptr.(unsafe.Pointer))[:]
}
func (b *bufferPool) Put(buf []byte) {
	b.Pool.Put(unsafe.Pointer(&buf[0]))
}

var globalBufferPool = &bufferPool{
	sync.Pool{
		New: func() any {
			buffer := new(bufferType)
			return unsafe.Pointer(&buffer[0])
		},
	},
}

func NewHttpUpstreamTransport(address string, director func(r *http.Request), transport http.RoundTripper) (u *Upstream) {
	u = &Upstream{Address: address}
	u.Healthy.Store(true)

	u.ReverseProxy = httputil.ReverseProxy{
		Director:  director,
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if err == http.ErrAbortHandler {
				netx.ResetRequestConn(w)
			} else if s, ok := err.(SuppressedHttpError); ok {
				s.ServeHTTP(w, r)
			} else {
				rctx := r.Context().Value(requestContextKey{}).(*requestContext)
				u.ErrorCount.Add(1)
				rctx.LoadBalancer.OnError(rctx, w, r, err)
			}
		},
		ModifyResponse: func(r *http.Response) error {
			ctx := r.Request.Context().Value(requestContextKey{}).(*requestContext)

			// Fast path for non-error responses.
			if !(400 <= r.StatusCode && r.StatusCode <= 599) {
				return nil
			}

			// If upstream requested to abort the request, do so.
			if r.StatusCode == 444 {
				ctx.Upstream.ClientErrorCount.Add(1)
				return http.ErrAbortHandler
			}

			// If the request is already canceled, do nothing.
			if ctx.Request.Context().Err() != nil {
				return nil
			}

			// Update metrics.
			if 500 <= r.StatusCode && r.StatusCode <= 599 {
				ctx.Upstream.ServerErrorCount.Add(1)
			} else if 400 <= r.StatusCode && r.StatusCode <= 499 {
				ctx.Upstream.ClientErrorCount.Add(1)
			}

			// Ask load balancer to handle the error.
			if hnd := ctx.LoadBalancer.OnErrorResponse(ctx, r); hnd != nil {
				go util.DrainClose(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(nil))
				return SuppressedHttpError{hnd}
			}
			return nil
		},
		BufferPool: globalBufferPool,
	}
	return
}
func (p *Upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.LoadFactor.Add(1)
	p.RequestCount.Add(1)
	defer p.LoadFactor.Add(-1)
	p.ReverseProxy.ServeHTTP(w, r)
}

func NewHttpUpstream(address string) (u *Upstream) {
	scheme := "http"
	if rest, ok := strings.CutPrefix(address, "https://"); ok {
		scheme = "https"
		address = rest
	} else if rest, ok := strings.CutPrefix(address, "http://"); ok {
		address = rest
	}

	if ipp := netx.ParseIPPort(address); ipp.IP.IsLoopback() {
		return NewHttpUpstreamTransport(
			address,
			func(r *http.Request) {
				r.URL.Scheme = scheme
				r.URL.Host = address
			},
			netx.LocalTransport,
		)
	} else {
		return NewHttpUpstreamTransport(
			address,
			func(r *http.Request) {
				r.URL.Scheme = scheme
				r.URL.Host = address
			},
			http.DefaultTransport,
		)
	}
}
