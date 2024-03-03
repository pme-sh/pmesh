package pmtp

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"sync"
)

type Stream interface {
	Close() error
	Write(h uint32, v *jrpcPacket) error
	ReadHeader() (h uint32, err error)
	ReadBody(v *jrpcPacket) error
}

type jrpcStream struct {
	c     net.Conn
	wb    *bufio.Writer
	rb    *bufio.Reader
	wlock sync.Mutex
}

var writerPool = sync.Pool{
	New: func() any {
		return bufio.NewWriterSize(nil, 64*1024)
	},
}

func (c *jrpcStream) Close() error {
	c.wlock.Lock()
	defer c.wlock.Unlock()
	if c.wb != nil {
		c.wb.Reset(nil)
		writerPool.Put(c.wb)
		c.wb = nil
	}
	return c.c.Close()
}

var noBody = []byte("\x00{}\n")

func (c *jrpcStream) Write(h uint32, v *jrpcPacket) error {
	c.wlock.Lock()
	defer c.wlock.Unlock()
	if c.wb == nil {
		return io.ErrClosedPipe
	}

	binary.Write(c.wb, binary.LittleEndian, h)
	c.wb.WriteString(v.ID)

	if n := len(v.Body); n == 0 {
		c.wb.Write(noBody)
	} else {
		noterm := v.Body[n-1] != '\n'
		c.wb.WriteByte(0)
		c.wb.Write(v.Body)
		if noterm {
			c.wb.WriteByte('\n')
		}
	}
	return c.wb.Flush()
}
func (c *jrpcStream) ReadHeader() (h uint32, err error) {
	err = binary.Read(c.rb, binary.LittleEndian, &h)
	return
}
func (c *jrpcStream) ReadBody(v *jrpcPacket) error {
	var err error
	v.ID, err = c.rb.ReadString(0)
	if err != nil {
		return err
	}
	if n := len(v.ID); n > 0 && v.ID[n-1] == 0 {
		v.ID = v.ID[:n-1]
	}

	v.Body, err = c.rb.ReadBytes('\n')
	return err
}

func newJrpcStream(conn net.Conn) *jrpcStream {
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
	}

	wb := writerPool.Get().(*bufio.Writer)
	wb.Reset(conn)

	c := &jrpcStream{
		c:  conn,
		wb: wb,
		rb: bufio.NewReader(conn),
	}
	return c
}
