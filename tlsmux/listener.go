package tlsmux

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"

	"get.pme.sh/pmesh/xlog"
)

var ErrListenerClosed = errors.New("listener closed")

// sublistener is a net.Listener that multiplexes connections to multiple
// sublisteners based on the protocol negotiated by the TLS handshake.
type sublistener struct {
	config *tls.Config
	mux    *Listener
	protos []string

	backlog chan net.Conn
	done    chan struct{}
	cleanup sync.RWMutex
}

// Accept waits for and returns the next connection to the listener.
func (sub *sublistener) Accept() (net.Conn, error) {
	select {
	case conn := <-sub.backlog:
		return conn, nil
	case <-sub.done:
		return nil, ErrListenerClosed
	}
}

// Close closes the listener.
func (sub *sublistener) Close() error {
	select {
	case <-sub.done:
		return nil
	default:
		close(sub.done)
	}
	sub.cleanup.Lock()
	defer sub.cleanup.Unlock()
	close(sub.backlog)
	for conn := range sub.backlog {
		conn.Close()
	}
	return sub.mux.close(sub)
}

// Addr returns the listener's network address.
func (sub *sublistener) Addr() net.Addr {
	return sub.mux.listener.Addr()
}

// handleConn is called by the Listener to handle a new connection.
func (sub *sublistener) handleConn(conn net.Conn) {
	sub.cleanup.RLock()
	defer sub.cleanup.RUnlock()

	select {
	case <-sub.done:
		conn.Close()
	case sub.backlog <- conn:
		return
	}
}

// Listener is the original listener that multiplexes connections to sublisteners.
type Listener struct {
	mu           sync.RWMutex
	sub          map[string]*sublistener // protocol -> listener
	listener     net.Listener
	closed       bool
	closeOnDrain bool
}

// close is called by a sublistener to remove itself from the Listener.
func (mx *Listener) close(sub *sublistener) error {
	mx.mu.Lock()
	defer mx.mu.Unlock()
	for _, proto := range sub.protos {
		if mx.sub[proto] == sub {
			delete(mx.sub, proto)
		}
	}
	if len(mx.sub) == 0 && mx.closeOnDrain {
		return mx.closeLocked()
	}
	return nil
}

// Listen creates a new sublistener for the given protocol and configuration.
func (mx *Listener) Listen(config *tls.Config, protos ...string) (net.Listener, error) {
	if len(config.NextProtos) == 0 && len(protos) == 0 {
		// If no protocols are specified, use the default protocol
		protos = []string{"*"}
	} else if len(protos) != 0 {
		// If no protocols are specified in the config, use the given protocols
		config = config.Clone()
		config.NextProtos = protos
	} else {
		// If protocols are specified in the config, use them
		protos = config.NextProtos
	}

	mx.mu.Lock()
	defer mx.mu.Unlock()
	if mx.closed {
		return nil, ErrListenerClosed
	}

	for _, proto := range protos {
		if mx.sub[proto] != nil {
			return nil, fmt.Errorf("listener already bound to protocol %q", proto)
		}
	}

	listener := &sublistener{
		config:  config,
		mux:     mx,
		protos:  protos,
		backlog: make(chan net.Conn, 8),
		done:    make(chan struct{}),
	}
	for _, proto := range protos {
		mx.sub[proto] = listener
	}
	return listener, nil
}

// findListener returns the sublistener for the given protocol.
func (mx *Listener) findListener(proto ...string) (sub *sublistener) {
	mx.mu.RLock()
	defer mx.mu.RUnlock()
	for _, proto := range proto {
		sub = mx.sub[proto]
		if sub != nil {
			return
		}
	}
	sub = mx.sub["*"]
	return
}

// GetConfigForClient returns the configuration for the given client hello.
// This is called by the tls package to resolve the configuration for a new connection.
func (mx *Listener) GetConfigForClient(hello *tls.ClientHelloInfo) (config *tls.Config, err error) {
	// Resolve the sublistener for this protocol
	sub := mx.findListener(hello.SupportedProtos...)
	if sub == nil {
		return nil, errors.New("no listener for this protocol")
	}

	// Resolve the final configuration this sublistener wants to use
	if sub.config.GetConfigForClient != nil {
		return sub.config.GetConfigForClient(hello)
	} else {
		return sub.config, nil
	}
}

// handleConn is called in the accept loop to handle a new connection.
func (mx *Listener) handleConn(conn net.Conn) {
	tlsc := conn.(*tls.Conn)
	state := tlsc.ConnectionState()
	err := tlsc.Handshake()
	if err != nil {
		xlog.Warn().Err(err).
			Any("addr", conn.RemoteAddr()).
			Str("sni", state.ServerName).
			Str("proto", state.NegotiatedProtocol).
			Msg("failed to handshake")
		conn.Close()
		return
	}

	proto := tlsc.ConnectionState().NegotiatedProtocol
	sub := mx.findListener(proto)
	if sub == nil {
		xlog.Warn().Err(err).
			Any("addr", conn.RemoteAddr()).
			Str("sni", state.ServerName).
			Str("proto", state.NegotiatedProtocol).
			Msg("no listener found for protocol")
		conn.Close()
		return
	}
	sub.handleConn(conn)
}

// Addr returns the listener's network address.
func (mx *Listener) Addr() net.Addr {
	return mx.listener.Addr()
}

// Close closes the listener.
func (mx *Listener) Close() error {
	mx.mu.Lock()
	defer mx.mu.Unlock()
	return mx.closeLocked()
}
func (mx *Listener) closeLocked() error {
	prev := mx.sub
	if prev != nil {
		mx.sub = nil
		go func() {
			for _, sub := range prev {
				sub.Close()
			}
		}()
	}
	mx.closed = true
	return mx.listener.Close()
}

// acceptLoop is the main accept loop for the listener.
func (mx *Listener) acceptLoop() {
	for {
		conn, err := mx.listener.Accept()
		if err != nil {
			return
		}
		go mx.handleConn(conn)
	}
}

// NewMuxListener creates a new TLS multiplexing listener.
func NewMuxListener(listener net.Listener) *Listener {
	mx := &Listener{
		sub: make(map[string]*sublistener),
	}
	ln := tls.NewListener(listener, &tls.Config{
		GetConfigForClient: mx.GetConfigForClient,
	})
	mx.listener = ln
	go mx.acceptLoop()
	return mx
}

// ListenMux starts a new listener on the given network and address.
func ListenMux(network, address string) (*Listener, error) {
	lcfg := &net.ListenConfig{KeepAlive: -1}
	l, err := lcfg.Listen(context.Background(), network, address)
	if err != nil {
		return nil, err
	}
	return NewMuxListener(l), nil
}
