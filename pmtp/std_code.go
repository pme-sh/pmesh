package pmtp

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"sync"
	"sync/atomic"

	"get.pme.sh/pmesh/xlog"

	"github.com/gorilla/websocket"
)

type stdCode interface {
	NewServerCodec(conn net.Conn) rpc.ServerCodec
	NewClientCodec(conn net.Conn) rpc.ClientCodec
}

type stdCodeAdapter struct {
	stdCode
	name string
}

func (a *stdCodeAdapter) String() string {
	return a.name
}
func (a *stdCodeAdapter) Serve(conn net.Conn, sv Server) {
	server := a.NewServerCodec(conn)
	defer server.Close()
	wg := &sync.WaitGroup{}
	sending := &sync.Mutex{}

	for {
		req := &rpc.Request{}
		if err := server.ReadRequestHeader(req); err != nil {
			if errors.Is(err, io.EOF) ||
				errors.Is(err, io.ErrClosedPipe) ||
				errors.Is(err, io.ErrUnexpectedEOF) ||
				errors.Is(err, net.ErrClosed) {
				break
			}
			if _, ok := err.(*websocket.CloseError); ok {
				break
			}
			xlog.Info().Err(err).Msg("server cannot read jrpc header")
			break
		}
		var body json.RawMessage
		err := server.ReadRequestBody(&body)
		wg.Add(1)
		go func() {
			defer wg.Done()

			var res any
			if err == nil {
				res, err = sv.ServeRPC(req.ServiceMethod, body)
			}

			response := &rpc.Response{
				ServiceMethod: req.ServiceMethod,
				Seq:           req.Seq,
			}
			sending.Lock()
			defer sending.Unlock()
			if err != nil {
				response.Error = err.Error()
				err = server.WriteResponse(response, nil)
			} else {
				err = server.WriteResponse(response, res)
			}
			if err != nil {
				xlog.Info().Err(err).Msg("server cannot encode response")
				return
			}
		}()
	}
	wg.Wait()
}
func (a *stdCodeAdapter) Open(conn net.Conn) (Client, error) {
	client := a.NewClientCodec(conn)
	return &stdClientAdapter{client: rpc.NewClientWithCodec(client), code: a}, nil
}

type stdClientAdapter struct {
	client *rpc.Client
	busyN  atomic.Int32
	closed atomic.Bool
	code   Code
}

func (c *stdClientAdapter) Code() Code {
	return c.code
}
func (c *stdClientAdapter) Call(method string, args any, reply any) error {
	if c.closed.Load() {
		return io.ErrClosedPipe
	}
	c.busyN.Add(1)
	defer c.busyN.Add(-1)
	return c.client.Call(method, args, reply)
}
func (c *stdClientAdapter) Busy() int {
	if c.closed.Load() {
		return -1
	}
	return int(c.busyN.Load())
}
func (c *stdClientAdapter) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	return c.client.Close()
}

type jsonCode struct{}

func (jsonCode) NewServerCodec(conn net.Conn) rpc.ServerCodec {
	return jsonrpc.NewServerCodec(conn)
}
func (jsonCode) NewClientCodec(conn net.Conn) rpc.ClientCodec {
	return jsonrpc.NewClientCodec(conn)
}

var CodeJSON Code = &stdCodeAdapter{jsonCode{}, "json-rpc"}
