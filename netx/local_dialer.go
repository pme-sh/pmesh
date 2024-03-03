package netx

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pme-sh/pmesh/config"
	"github.com/pme-sh/pmesh/subnet"
	"github.com/pme-sh/pmesh/xlog"

	"golang.org/x/net/http2"
)

type TracedConn struct {
	net.Conn
}

var localDialerSocketCount atomic.Int64
var DebugSocketCount = false

func GetLocalDialerSocketCount() int64 {
	return localDialerSocketCount.Load()
}

func NewTracedConn(conn net.Conn) *TracedConn {
	x := localDialerSocketCount.Add(1)
	if x&255 == 0 && x != 0 && DebugSocketCount {
		xlog.Info().Int64("n", x).Stringer("local", conn.LocalAddr()).Stringer("remote", conn.RemoteAddr()).Msg("Socket created")
	}
	return &TracedConn{Conn: conn}
}
func (c *TracedConn) Close() error {
	x := localDialerSocketCount.Add(-1)
	if x&255 == 0 && x != 0 && DebugSocketCount {
		xlog.Info().Int64("n", x).Stringer("local", c.Conn.LocalAddr()).Stringer("remote", c.Conn.RemoteAddr()).Msg("Socket closed")
	}
	return c.Conn.Close()
}

type LocalDialer struct {
	net.Dialer
}

var localDialerAllocator = sync.OnceValue(func() *subnet.Allocator {
	return subnet.NewAllocator(*config.DialerSubnet, true)
})

func MakeLocalDialer(d *net.Dialer) *net.Dialer {
	if d == nil {
		d = &net.Dialer{}
	}
	if d.Timeout == 0 {
		d.Timeout = 15 * time.Second
	}
	if d.KeepAlive == 0 {
		d.KeepAlive = -1
	}
	if runtime.GOOS != "darwin" {
		ipv4 := localDialerAllocator().Generate()
		d.LocalAddr = &net.TCPAddr{IP: ipv4, Port: 0}
	}
	return d
}

func (d LocalDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := d.Dialer
	conn, err := MakeLocalDialer(&dialer).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return NewTracedConn(conn), nil
}

func MakeLocalTransport(idle int, max int, opts *http.Transport) *http.Transport {
	var ldial LocalDialer
	opts.DisableCompression = true
	opts.MaxIdleConns = idle
	opts.MaxIdleConnsPerHost = idle
	opts.MaxConnsPerHost = max
	opts.IdleConnTimeout = 10 * time.Second
	opts.DialContext = ldial.DialContext
	return opts
}

var LocalTransport = MakeLocalTransport(16384, 0, &http.Transport{
	TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
	ResponseHeaderTimeout: 10 * time.Second,
})
var LocalH2Transport = &http2.Transport{
	DisableCompression: true,
	DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
		cfg.InsecureSkipVerify = true
		tdial := tls.Dialer{
			Config:    cfg,
			NetDialer: MakeLocalDialer(nil),
		}
		conn, err := tdial.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return NewTracedConn(conn), nil
	},
}

func ResetConn(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.Close()
	} else {
		conn.Close()

	}
}
func ResetRequestConn(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	if conn, _, err := rc.Hijack(); err == nil {
		ResetConn(conn)
	}
	panic(http.ErrAbortHandler)
}
