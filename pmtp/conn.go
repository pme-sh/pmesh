package pmtp

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/pme-sh/pmesh/config"
	"github.com/pme-sh/pmesh/lru"
	"github.com/pme-sh/pmesh/security"

	"github.com/gorilla/websocket"
)

var RpcCodeList = []Code{
	YamuxCode{CodeJRPC}, CodeJRPC,
	YamuxCode{CodeJSON}, CodeJSON,
}

type rpcServerAdapter[Arg any] struct {
	Code
	Sv func(conn net.Conn, code Code, arg Arg)
}

func (c rpcServerAdapter[Arg]) Serve(conn net.Conn, o Arg) {
	c.Sv(conn, c.Code, o)
}

func MakeRPCServer[Arg any](sv func(conn net.Conn, code Code, arg Arg)) UpgradeServer[*rpcServerAdapter[Arg], Arg] {
	list := make([]*rpcServerAdapter[Arg], len(RpcCodeList))
	for i, c := range RpcCodeList {
		list[i] = &rpcServerAdapter[Arg]{c, sv}
	}
	r := MakeUpgradeServer(list...)
	r.Protocols[""] = &rpcServerAdapter[Arg]{CodeJSON, sv}
	return r
}

var RpcUpgradeServer = MakeUpgradeServer(RpcCodeList...)

func FindCodeByName(name string) Code {
	if name == "" {
		return nil
	}
	return RpcUpgradeServer.Protocols[name]
}

// URL parsing.
type ConnURL struct {
	Path           string
	RawQuery       string
	Host           string
	Secret         string
	DisablePoolMux bool
	DisableYamux   bool
	Code           Code
	Websocket      bool
	TLS            bool
}

func ConvertURL(from *url.URL) (u *ConnURL, err error) {
	u = new(ConnURL)
	u.Path = from.Path
	u.RawQuery = from.RawQuery
	u.Host = from.Host
	u.Secret = config.Get().Secret
	u.Code = CodeJRPC
	u.Websocket = from.Scheme == "pmtp+ws"
	u.TLS = true

	switch from.Scheme {
	case "http", "ws":
		u.TLS = false
		u.Websocket = from.Scheme == "ws"
	case "https", "wss":
		u.TLS = true
		u.Websocket = from.Scheme == "wss"
	case "pmtp", "pmtp+ws":

		if port := from.Port(); port == "80" {
			u.TLS = false
		} else if port != "443" {
			switch from.Hostname() {
			case "localhost", "127.0.0.1", "pm3", "::1":
				u.TLS = false
			}
		}
	default:
		err = fmt.Errorf("invalid scheme: %s", from.Scheme)
		return
	}

	// Validate the URL.
	//
	if u.Path == "" || u.Path == "/" {
		u.Path = "/connect"
	}
	if from.User != nil {
		if pw, _ := from.User.Password(); pw != "" {
			u.Secret = pw
		} else if us := from.User.Username(); us != "" {
			u.Secret = us
		}
		from.User = nil
	}

	// Parse options.
	//
	if q, _ := url.ParseQuery(u.RawQuery); len(q) > 0 {
		if v := q.Get("pool"); v != "" {
			u.DisablePoolMux = v != "1"
			q.Del("pool")
		}
		if v := q.Get("yamux"); v != "" {
			u.DisableYamux = v != "1"
			q.Del("yamux")
		}
		if v := q.Get("code"); v != "" {
			u.Code = FindCodeByName(v)
			if u.Code == nil {
				err = fmt.Errorf("invalid code: %s", v)
				return
			}
			q.Del("code")
		}
		u.RawQuery = q.Encode()
	} else {
		u.RawQuery = ""
	}
	return
}
func ParseURL(urlStr string) (u *ConnURL, err error) {
	url, err := url.Parse(urlStr)
	if err != nil {
		return
	}
	return ConvertURL(url)
}

func (u *ConnURL) URL() *url.URL {
	url := &url.URL{
		Scheme:   "http",
		Host:     u.Host,
		Path:     u.Path,
		RawQuery: u.RawQuery,
	}
	if u.Websocket {
		url.Scheme = "ws"
	}
	if u.TLS {
		url.Scheme = "https"
		if u.Websocket {
			url.Scheme = "wss"
		}
	}
	return url
}
func (u *ConnURL) Dialer() *Dialer {
	return NewDialer(u.Secret)
}

// Dialer is a dialer for RPC connections over HTTP and WebSocket.
type Dialer struct {
	NetDialContext  func(ctx context.Context, network string, address string) (net.Conn, error)
	TLSClientConfig *tls.Config
}

func NewDialer(secret string) *Dialer {
	return &Dialer{
		TLSClientConfig: security.CreateMutualAuthenticator(secret, "http/1.1").Client,
	}
}

func (d *Dialer) Dial(u *ConnURL) (Client, error) {
	return d.DialContext(context.Background(), u)
}

func (d *Dialer) DialContext(ctx context.Context, u *ConnURL) (Client, error) {
	if u.DisablePoolMux {
		return d.dialContext(ctx, u.URL(), u.Code)
	}
	c := u.Code
	if _, ok := c.(YamuxCode); !ok && !u.DisableYamux {
		c = YamuxCode{c}
	}
	return NewPoolMux(c, func(c Code) (Client, error) {
		return d.dialContext(ctx, u.URL(), c)
	})
}

func (d *Dialer) netDialContext(ctx context.Context, network, address string) (c net.Conn, err error) {
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, "80")
	}

	if d.NetDialContext != nil {
		c, err = d.NetDialContext(ctx, network, address)
	} else {
		var dialer net.Dialer
		c, err = dialer.DialContext(ctx, network, address)
	}
	return
}
func (d *Dialer) tlsDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(address, "443")
	}

	rawConn, err := d.netDialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}

	cfg := d.TLSClientConfig
	if cfg == nil {
		cfg = &tls.Config{}
	}
	conn := tls.Client(rawConn, cfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, err
	}
	return conn, nil
}
func (d *Dialer) dialContext(ctx context.Context, u *url.URL, code Code) (Client, error) {
	if u.Scheme == "ws" || u.Scheme == "wss" {
		return d.dialWebsocket(ctx, u, code)
	} else if u.Scheme == "http" || u.Scheme == "https" {
		conn, err := d.DialUpgrade(ctx, u, code.String())
		if err != nil {
			return nil, err
		}
		return code.Open(conn)
	}
	return nil, fmt.Errorf("jrpc: unsupported scheme %q", u.Scheme)
}
func (d *Dialer) DialUpgrade(ctx context.Context, u *url.URL, proto string) (conn net.Conn, err error) {
	// Send the request.
	req := &http.Request{
		Method: "GET",
		URL:    u,
		Header: http.Header{
			"Connection": {"Upgrade"},
			"Upgrade":    {proto},
		},
	}
	conn, resp, err := d.RoundTrip(req.WithContext(ctx))
	if err != nil {
		return
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		resmsg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("jrpc: failed to upgrade, status %d: %s", resp.StatusCode, resmsg)
	}
	// Check the response.
	if resp.Header.Get("Connection") != "Upgrade" {
		conn.Close()
		return nil, fmt.Errorf("jrpc: failed to upgrade, Connection header is %q", resp.Header.Get("Connection"))
	}
	if resp.Header.Get("Upgrade") != proto {
		conn.Close()
		return nil, fmt.Errorf("jrpc: failed to upgrade, Upgrade header is %q", resp.Header.Get("Upgrade"))
	}
	return conn, nil
}
func (d *Dialer) dialWebsocket(ctx context.Context, u *url.URL, code Code) (cli Client, err error) {
	wsd := websocket.Dialer{
		Subprotocols:      []string{code.String()},
		ReadBufferSize:    32 * 1024,
		WriteBufferSize:   32 * 1024,
		NetDialContext:    d.netDialContext,
		NetDialTLSContext: d.tlsDialContext,
		TLSClientConfig:   d.TLSClientConfig,
	}
	if u.Scheme == "https" || u.Scheme == "wss" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	conn, _, err := wsd.DialContext(ctx, u.String(), http.Header{
		"Host": []string{"pm3"},
	})
	if err != nil {
		return
	}
	wrapper := &wsrwc{conn: conn}
	cli, err = code.Open(wrapper)
	if err != nil {
		conn.Close()
	}
	return
}
func (d *Dialer) RoundTrip(req *http.Request) (conn net.Conn, resp *http.Response, err error) {
	req.Host = "pm3"
	req.Proto = "HTTP/1.1"
	req.ProtoMajor = 1
	req.ProtoMinor = 1

	// Dial the connection.
	if req.URL.Scheme == "http" || req.URL.Scheme == "ws" {
		if conn, err = d.netDialContext(req.Context(), "tcp", req.URL.Host); err != nil {
			return
		}
	} else {
		if conn, err = d.tlsDialContext(req.Context(), "tcp", req.URL.Host); err != nil {
			return
		}

		// Check for HTTP/2 CONNECT.
		tls, ok := conn.(*tls.Conn)
		if !ok {
			conn.Close()
			return nil, nil, fmt.Errorf("jrpc: failed to upgrade, not a TLS connection")
		} else if tls.ConnectionState().NegotiatedProtocol == "h2" {
			conn.Close()
			return nil, nil, fmt.Errorf("jrpc: failed to upgrade, HTTP/2 CONNECT is not supported")
		}
	}

	// Roundtrip the request.
	if err = req.Write(conn); err != nil {
		conn.Close()
	} else if resp, err = http.ReadResponse(bufio.NewReader(conn), req); err != nil {
		conn.Close()
	}
	return
}

const DefaultURL = "pmtp://127.0.0.1"

func Dial(url string) (Client, error) {
	return DialContext(context.Background(), url)
}
func DialContext(ctx context.Context, url string) (Client, error) {
	u, err := ParseURL(url)
	if err != nil {
		return nil, err
	}
	return u.Dialer().DialContext(ctx, u)
}

// Shared client is a reference counted client that can
// be shared between multiple connections.
type sharedClient struct {
	Client
	entry atomic.Pointer[lru.Entry[*sharedClient]]
}

func (c *sharedClient) Close() error {
	if e := c.entry.Swap(nil); e != nil {
		e.Release()
		return c.Client.Close()
	}
	return nil
}

// Connection pool
var sharedClientPool = lru.Cache[string, *sharedClient]{
	Expiry:          time.Minute,
	CleanupInterval: 5 * time.Minute,
	New: func(uri string, entry *lru.Entry[*sharedClient]) (err error) {
		conn, err := Dial(uri)
		if err == nil {
			tconn := &sharedClient{Client: conn}
			tconn.entry.Store(entry)
			entry.Value = tconn
		}
		return
	},
	Evict: func(_ string, cli *sharedClient) {
		cli.Client.Close()
	},
}

// Connect to a server using the shared client pool.
func Connect(uri string) (Client, error) {
	cli, err := sharedClientPool.Get(uri)
	if err != nil {
		return nil, err
	}
	cli.entry.Load().Acquire()
	return cli, nil
}
