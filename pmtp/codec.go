package pmtp

import (
	"encoding/json"
	"errors"
	"net"
)

type Client interface {
	Busy() int // number of pending requests, -1 if closed
	Call(method string, args any, reply any) error
	Close() error
	Code() Code
}

type Code interface {
	Open(conn net.Conn) (Client, error)
	Serve(conn net.Conn, sv Server)
	String() string
}

type Server interface {
	ServeRPC(method string, body json.RawMessage) (any, error)
}
type ServerFunc func(method string, body json.RawMessage) (any, error)

func (f ServerFunc) ServeRPC(method string, body json.RawMessage) (any, error) {
	return f(method, body)
}

type nullServer struct{}

func (nullServer) ServeRPC(string, json.RawMessage) (any, error) {
	return nil, errors.New("jrpc: method not found")
}
