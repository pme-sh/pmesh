package pmtp

import (
	"net"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

type YamuxCode struct {
	Code
}

var yamuxConfig = &yamux.Config{
	AcceptBacklog:          256,
	EnableKeepAlive:        true,
	KeepAliveInterval:      30 * time.Second,
	ConnectionWriteTimeout: 10 * time.Second,
	MaxStreamWindowSize:    512 * 1024,
	StreamCloseTimeout:     5 * time.Minute,
	StreamOpenTimeout:      75 * time.Second,
	LogOutput:              os.Stderr,
}

type yamuxClient struct {
	Client
}

func (c yamuxClient) Code() Code {
	return YamuxCode{c.Client.Code()}
}

func yamuxCloseGraceful(session *yamux.Session) {
	session.GoAway()
	go func() {
		select {
		case <-time.After(yamuxConfig.StreamCloseTimeout):
		case <-session.CloseChan():
		}
		session.Close()
	}()
}

func (c YamuxCode) Open(conn net.Conn) (Client, error) {
	session, err := yamux.Client(conn, yamuxConfig)
	if err != nil {
		return nil, err
	}
	return NewPoolMuxConfig(PoolMuxConfig{
		Code: c.Code,
		Connect: func(c Code) (Client, error) {
			stream, err := session.Open()
			if err != nil {
				return nil, err
			}
			cli, err := c.Open(stream)
			if err != nil {
				stream.Close()
				return nil, err
			}
			return yamuxClient{cli}, nil
		},
		MaxConns:   yamuxConfig.AcceptBacklog,
		Preconnect: 1,
		AfterClose: func() {
			go yamuxCloseGraceful(session)
		},
	})
}
func (c YamuxCode) Serve(conn net.Conn, sv Server) {
	session, err := yamux.Server(conn, yamuxConfig)
	if err != nil {
		conn.Close()
		return
	}
	defer yamuxCloseGraceful(session)

	wg := &sync.WaitGroup{}
	for {
		stream, err := session.Accept()
		if err != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Code.Serve(stream, sv)
		}()
	}
	wg.Wait()
}
func (c YamuxCode) String() string { return c.Code.String() + "+yamux" }

var CodeYRPC Code = YamuxCode{CodeJRPC}
