package pmtp

import (
	"io"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// PoolMux is a pool of pmtp.Clients, if a code is of type ymux,
// the pool will be initially populated with the underlying code and
// switch to a shared ymux connection when the pool is drained.
type PoolMux struct {
	Connect func(c Code) (Client, error)
	busyN   atomic.Int32
	closed  atomic.Bool
	code    Code

	connsMu    sync.Mutex
	conns      []Client
	shared     Client
	queue      chan Client
	afterClose func()
}

type PoolMuxConfig struct {
	// Code is the code of the pool.
	Code Code
	// Connect is the function used to create a new connection.
	Connect func(c Code) (Client, error)
	// MaxConns is the maximum number of connections in the pool.
	MaxConns int
	// Preconnect is the number of connections to preconnect.
	Preconnect int
	// AfterClose is a function called after the pool is closed.
	AfterClose func()
}

// NewPoolMuxConfig creates a new pool of pmtp.Clients with a given configuration.
func NewPoolMuxConfig(cfg PoolMuxConfig) (*PoolMux, error) {
	cp := &PoolMux{
		Connect:    cfg.Connect,
		code:       cfg.Code,
		conns:      make([]Client, cfg.MaxConns),
		queue:      make(chan Client, cfg.MaxConns),
		afterClose: cfg.AfterClose,
	}
	if err := cp.Preconnect(max(1, cfg.Preconnect)); err != nil {
		cp.Close()
		return nil, err
	}
	return cp, nil
}

// NewPoolMux creates a new pool of pmtp.Clients with a default size.
func NewPoolMux(code Code, connect func(c Code) (Client, error)) (*PoolMux, error) {
	return NewPoolMuxConfig(PoolMuxConfig{
		Code:       code,
		Connect:    connect,
		MaxConns:   64,
		Preconnect: 2,
	})
}

// Returns the MaxConns of the pool.
func (p *PoolMux) MaxConns() int {
	return len(p.conns)
}

// Preconnect ensures that the pool is prepopulated with a given number of connections.
func (p *PoolMux) Preconnect(n int) error {
	p.connsMu.Lock()
	defer p.connsMu.Unlock()

	errgroup := &errgroup.Group{}
	for i, conn := range p.conns {
		if i >= n {
			break
		}
		if conn != nil {
			continue
		}
		errgroup.Go(func() error {
			cli, err := p.newUniqueConnection()
			if err != nil {
				return err
			}
			p.conns[i] = cli
			select {
			case p.queue <- cli:
			default:
			}
			return nil
		})
	}
	return errgroup.Wait()
}

// Acquire returns a pmtp.Client from the pool, if the pool is closed, it returns io.ErrClosedPipe.
// If the pool is drained, it will share another connection or return the muxed connection.
func (p *PoolMux) Acquire() (cli Client, unique bool, err error) {
	if p.closed.Load() {
		return nil, false, io.ErrClosedPipe
	}
	cli, err = p.getUniqueClient()
	if err != nil {
		return
	}
	if cli != nil {
		unique = true
		return
	}
	cli, unique, err = p.getSharedClient()
	return
}

// Release returns a pmtp.Client to the pool, if the client is unique and not closed,
// it will be added to the pool.
func (p *PoolMux) Release(cli Client, unique bool) {
	if cli != nil && unique {
		if cli.Busy() == -1 {
			return
		}
		select {
		case p.queue <- cli:
		default:
		}
	}
}

// Call is a wrapper around pmtp.Client.Call, it acquires a client from the pool,
func (p *PoolMux) Call(method string, args, reply any) error {
	cli, unique, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(cli, unique)
	p.busyN.Add(1)
	defer p.busyN.Add(-1)
	return cli.Call(method, args, reply)
}

// Implement the pmtp.Client interface.
// Busy returns the number of busy connections in the pool.
func (p *PoolMux) Busy() int {
	if p.closed.Load() {
		return -1
	}
	return int(p.busyN.Load())
}

// Close closes the pool and all of its connections.
func (p *PoolMux) Close() error {
	p.connsMu.Lock()
	defer p.connsMu.Unlock()
	if !p.closed.Swap(true) {
		for i, c := range p.conns {
			if c != nil {
				c.Close()
				p.conns[i] = nil
			}
		}
		if p.shared != nil {
			p.shared.Close()
			p.shared = nil
		}
		if p.afterClose != nil {
			p.afterClose()
		}
	}
	return nil
}

// Code returns the code of the pool.
func (p *PoolMux) Code() Code {
	return p.code
}

// Creates a new non-muxed connection.
func (p *PoolMux) newUniqueConnection() (cli Client, err error) {
	code := p.code
	if mux, ok := code.(YamuxCode); ok {
		code = mux.Code
	}
	return p.Connect(code)
}

// Returns a shared connection whether muxed or busy.
func (p *PoolMux) getSharedClient() (cli Client, unique bool, err error) {
	p.connsMu.Lock()
	defer p.connsMu.Unlock()

	// If the code is of type yamux, we will use a shared connection.
	if _, ok := p.code.(YamuxCode); ok {
		if p.shared == nil || p.shared.Busy() == -1 {
			p.shared, err = p.Connect(p.code)
		}
		cli = p.shared
		return
	}

	// Otherwise, find the least busy connection or fill the first empty slot with a new connection.
	best := 0
	var firstNull *Client
	for i, c := range p.conns {
		if c != nil {
			if n := c.Busy(); n == -1 {
				p.conns[i] = nil
				if firstNull == nil {
					firstNull = &p.conns[i]
				}
			} else if cli == nil || n < best {
				best = n
				cli = c
			}
		} else {
			if firstNull == nil {
				firstNull = &p.conns[i]
			}
		}
	}
	if cli == nil {
		cli, err = p.newUniqueConnection()
		*firstNull = cli
		unique = true
	}
	return
}

// Returns a unique connection from the pool or creates a new one.
func (p *PoolMux) getUniqueClient() (cli Client, err error) {
	for {
		select {
		case cli = <-p.queue:
			if cli.Busy() != -1 {
				return
			}
			cli = nil
		default:
			p.connsMu.Lock()
			defer p.connsMu.Unlock()
			for i, c := range p.conns {
				if c != nil {
					if n := c.Busy(); n == 0 {
						return c, nil
					} else if n == -1 {
						p.conns[i] = nil
					} else {
						continue
					}
				}

				cli, err = p.newUniqueConnection()
				if err != nil {
					return
				}
				p.conns[i] = cli
				return
			}
			return nil, nil
		}
	}
}
