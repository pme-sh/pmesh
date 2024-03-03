package pmtp

import (
	"cmp"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
)

type jrpcDuplex struct {
	io  Stream
	sv  Server
	seq atomic.Uint32

	elock   sync.Mutex
	pending map[uint32]*jrpcPacket
	err     error
	closed  atomic.Bool
	busyN   atomic.Int32
}

var errClientClosed = errors.New("jrpc: client closed")

func (d *jrpcDuplex) Code() Code {
	return CodeJRPC
}
func (d *jrpcDuplex) Err() error {
	if d.closed.Load() {
		return d.err
	}
	return nil
}
func (d *jrpcDuplex) Shutdown(e error) error {
	// If the connection is already closed, return immediately.
	if d.closed.Load() {
		if d.err == errClientClosed {
			return nil
		}
		return d.err
	}

	// Acquire the lock.
	//
	d.elock.Lock()
	defer d.elock.Unlock()
	if d.err != nil {
		if d.err == errClientClosed {
			return nil
		}
		return d.err
	}

	// Reject all pending requests.
	for _, p := range d.pending {
		close(p.Signal)
	}

	// Set the error.
	d.err = cmp.Or(e, errClientClosed)
	d.pending = nil
	d.closed.Store(true)
	return d.io.Close()
}
func (d *jrpcDuplex) Close() error {
	return d.Shutdown(nil)
}
func (d *jrpcDuplex) Busy() int {
	if d.closed.Load() {
		return -1
	}
	return int(d.busyN.Load())
}

// Gets a packet from the pending map and removes it for resolution.
func (d *jrpcDuplex) pop(s jrpcSeq) (p *jrpcPacket, ok bool) {
	// Acquire state lock, ensure it is not closed.
	if d.closed.Load() {
		return nil, false
	}
	d.elock.Lock()
	defer d.elock.Unlock()
	if d.err != nil {
		return nil, false
	}

	// Resolve the packet.
	s &= FlagMask
	p, ok = d.pending[uint32(s)]
	if ok {
		delete(d.pending, uint32(s))
	}
	return
}

// Puts a packet into the pending map.
func (d *jrpcDuplex) promise(s jrpcSeq, p *jrpcPacket) error {
	// Acquire state lock, ensure it is not closed.
	if d.closed.Load() {
		return d.err
	}
	d.elock.Lock()
	defer d.elock.Unlock()
	if d.err != nil {
		return d.err
	}

	// Make sure the packet has a signal channel.
	p.Signal = make(chan jrpcSeq)

	// Put the packet into the pending map.
	d.pending[uint32(s)] = p
	return nil
}

// Serves a request and sends the response.
func (d *jrpcDuplex) serve(s jrpcSeq, p *jrpcPacket) {
	defer p.Release()
	res, err := d.sv.ServeRPC(p.ID, p.Body)
	s &= FlagMask
	s |= FlagReply
	if err != nil {
		e := err.Error()
		if e == "" {
			e = "error"
		}
		p.Encode(e, nil)
	} else {
		p.Encode("", res)
	}
	if err := d.io.Write(uint32(s), p); err != nil {
		d.Shutdown(err)
	}
}

// Sends a request and waits for the response.
func (d *jrpcDuplex) roundtrip(p *jrpcPacket) error {
	// Prepare the request.
	s := jrpcSeq(d.seq.Add(1)) & FlagMask
	if err := d.promise(s, p); err != nil {
		return err
	}

	// Write the request.
	if err := d.io.Write(uint32(s), p); err != nil {
		d.Shutdown(err)
		return err
	}

	// Wait for the response.
	s = <-p.Signal

	// If reply flag is not set, this is the connection error.
	if s&FlagReply != FlagReply {
		return cmp.Or(d.Err(), errClientClosed)
	}

	// If packet has an ID field, this is the error message.
	if p.ID != "" {
		return errors.New(string(p.Body))
	} else {
		return nil
	}
}

// Input loop.
func (d *jrpcDuplex) input() {
	var err error
	defer func() {
		d.Shutdown(err)
	}()

	for {
		// Read the header.
		var seqi uint32
		if seqi, err = d.io.ReadHeader(); err != nil {
			break
		}
		seq := jrpcSeq(seqi)

		// If this is a request, decode the packet and serve the request.
		if seq&FlagReply != FlagReply {
			p := newJrpcPacket()
			if err = d.io.ReadBody(p); err != nil {
				break
			}
			go d.serve(seq, p)
			continue
		}

		// If this is a reply, find the pending request
		if pending, ok := d.pop(seq); !ok {
			// If the request is not found, read and discard the packet
			var p jrpcPacket
			if err = d.io.ReadBody(&p); err != nil {
				break
			}
		} else {
			// If the request is found, decode the packet and signal the request
			pending.Body = pending.Body[:0]
			pending.ID = ""
			if err = d.io.ReadBody(pending); err != nil {
				break
			}
			pending.Signal <- seq
		}
	}
}

func newJrpcDuplex(conn net.Conn, sv Server) *jrpcDuplex {
	if sv == nil {
		sv = nullServer{}
	}
	d := &jrpcDuplex{
		io:      newJrpcStream(conn),
		sv:      sv,
		pending: make(map[uint32]*jrpcPacket),
	}
	return d
}

func (d *jrpcDuplex) Call(method string, args any, reply any) error {
	d.busyN.Add(1)
	defer d.busyN.Add(-1)

	// Prepare Packet.
	p := newJrpcPacket()
	defer p.Release()
	if err := p.Encode(method, args); err != nil {
		return err
	}

	// Send and receive Packet.
	if err := d.roundtrip(p); err != nil {
		return err
	}

	// Decode the response.
	if reply == nil {
		return nil
	} else {
		if raw, ok := reply.(*json.RawMessage); ok {
			*raw = p.Steal()
			return nil
		} else {
			return json.Unmarshal(p.Body, reply)
		}
	}
}

type jrpcCode struct{}

func (jrpcCode) Open(conn net.Conn) (Client, error) {
	dup := newJrpcDuplex(conn, nil)
	go dup.input()
	return dup, nil
}
func (jrpcCode) Serve(conn net.Conn, sv Server) {
	dup := newJrpcDuplex(conn, sv)
	defer dup.Close()
	dup.input()
}
func (jrpcCode) String() string { return "jrpc" }

var CodeJRPC Code = jrpcCode{}
